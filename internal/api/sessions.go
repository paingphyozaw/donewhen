package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johnreginald/donewhen/internal/auth"
	"github.com/johnreginald/donewhen/internal/models"
	"github.com/johnreginald/donewhen/internal/store"
)

// Agent sessions (BIZ-278): Claude Code sessions with their token usage.
// `donewhen sessions push` saves them; the GETs read them for the workspace.

// sessionReq is the PUT body. Unknown fields are refused, so a client cannot
// send message text along by mistake.
type sessionReq struct {
	ProjectPath string            `json:"projectPath"`
	GitBranch   string            `json:"gitBranch"`
	IssueKeys   []string          `json:"issueKeys"`
	StartedAt   time.Time         `json:"startedAt"`
	EndedAt     time.Time         `json:"endedAt"`
	Messages    int               `json:"messages"`
	Usage       []sessionUsageReq `json:"usage"`
}

type sessionUsageReq struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
}

func writeInvalidSession(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg, "code": "invalid_session"})
}

func (s *Server) handlePutSession(w http.ResponseWriter, r *http.Request) {
	var req sessionReq
	if err := readJSON(r, &req); err != nil {
		writeInvalidSession(w, "invalid body: "+err.Error())
		return
	}
	in := models.AgentSession{
		ExternalID:  r.PathValue("externalId"),
		ProjectPath: req.ProjectPath,
		GitBranch:   req.GitBranch,
		IssueKeys:   req.IssueKeys,
		StartedAt:   req.StartedAt,
		EndedAt:     req.EndedAt,
		Messages:    req.Messages,
	}
	for _, u := range req.Usage {
		in.Usage = append(in.Usage, models.SessionUsage{
			Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
		})
	}
	saved, err := s.svc.SaveSession(r.Context(), ws(r), in, auth.ActorFrom(r.Context()))
	if errors.Is(err, store.ErrInvalid) {
		writeInvalidSession(w, strings.TrimPrefix(err.Error(), "invalid: "))
		return
	}
	if handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	f, err := sessionFilter(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.store.ListSessions(r.Context(), ws(r), f)
	if handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSessionSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := sessionFilter(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	group := q.Get("group")
	if group == "" {
		group = store.SessionGroupDay
	}
	rows, err := s.store.SessionSummary(r.Context(), ws(r), f, group)
	if handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// sessionFilter reads from, to, project, issue and limit. from and to take a
// date (YYYY-MM-DD, UTC) or an RFC 3339 time; a date in `to` includes that
// whole day.
func sessionFilter(q url.Values) (store.SessionFilter, error) {
	var f store.SessionFilter
	if v := q.Get("from"); v != "" {
		t, _, err := parseSessionTime(v)
		if err != nil {
			return f, errors.New("from must be a date (YYYY-MM-DD) or an RFC 3339 time")
		}
		f.From = t
	}
	if v := q.Get("to"); v != "" {
		t, isDate, err := parseSessionTime(v)
		if err != nil {
			return f, errors.New("to must be a date (YYYY-MM-DD) or an RFC 3339 time")
		}
		if isDate {
			f.Before = t.AddDate(0, 0, 1)
		} else {
			f.Before = t.Add(time.Microsecond) // inclusive at Postgres precision
		}
	}
	f.Project = q.Get("project")
	f.Issue = strings.ToUpper(strings.TrimSpace(q.Get("issue")))
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return f, errors.New("limit must be a positive number")
		}
		f.Limit = n
	}
	return f, nil
}

func parseSessionTime(v string) (t time.Time, isDate bool, err error) {
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t, true, nil
	}
	t, err = time.Parse(time.RFC3339, v)
	return t, false, err
}
