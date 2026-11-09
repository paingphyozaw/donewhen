package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnreginald/donewhen/internal/models"
)

// `donewhen sessions push` (BIZ-278) reads Claude Code transcripts and sends
// one PUT /api/sessions/{id} per session. It sends numbers and metadata only:
// the parser decodes nothing but the fields below, so message text, tool
// input and tool output never reach the request.

const sessionsUsage = `usage:
  donewhen sessions push --file <transcript.jsonl>
  donewhen sessions push --dir <dir> [--since YYYY-MM-DD]

env: DONEWHEN_URL, DONEWHEN_TOKEN (or ~/.config/donewhen/token), DONEWHEN_WORKSPACE
`

// transcriptLine is everything the importer reads from one jsonl line.
type transcriptLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Timestamp string `json:"timestamp"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type record struct {
	line transcriptLine
	at   time.Time
	ok   bool // at was parsed
}

// readRecords decodes every line it can; a line that is not JSON is skipped.
func readRecords(r io.Reader) ([]record, error) {
	var out []record
	br := bufio.NewReader(r)
	for {
		raw, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(raw)) > 0 {
			var l transcriptLine
			if json.Unmarshal(raw, &l) == nil && l.SessionID != "" {
				at, terr := time.Parse(time.RFC3339Nano, l.Timestamp)
				out = append(out, record{line: l, at: at, ok: terr == nil})
			}
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// parseTranscript sums one session from its main transcript and the
// transcripts of its subagents (which carry the same sessionId). The session
// is the one whose id is the file name stem; when no line has that id, it is
// the first session in the main transcript. Lines of other sessions are
// ignored. Usage is counted once per message id, since Claude Code writes one
// line per content block of the same message. ok is false when no assistant
// message was found.
func parseTranscript(stem string, main io.Reader, subagents ...io.Reader) (models.AgentSession, bool, error) {
	var recs []record
	for i, r := range append([]io.Reader{main}, subagents...) {
		got, err := readRecords(r)
		if err != nil {
			return models.AgentSession{}, false, err
		}
		if i == 0 {
			found := false
			for _, rec := range got {
				if rec.line.SessionID == stem {
					found = true
					break
				}
			}
			if !found && len(got) > 0 {
				stem = got[0].line.SessionID
			}
		}
		recs = append(recs, got...)
	}

	s := models.AgentSession{ExternalID: stem}
	type msg struct {
		model string
		u     models.SessionUsage
	}
	byID := map[string]msg{}
	var order []string
	var lastBranchAt time.Time
	seenKey := map[string]bool{}
	anon := 0
	for _, rec := range recs {
		l := rec.line
		if l.SessionID != stem {
			continue
		}
		if s.ProjectPath == "" && l.Cwd != "" {
			s.ProjectPath = l.Cwd
		}
		if rec.ok {
			if s.StartedAt.IsZero() || rec.at.Before(s.StartedAt) {
				s.StartedAt = rec.at
			}
			if rec.at.After(s.EndedAt) {
				s.EndedAt = rec.at
			}
		}
		if l.GitBranch != "" && l.GitBranch != "HEAD" {
			if s.GitBranch == "" || !rec.at.Before(lastBranchAt) {
				s.GitBranch, lastBranchAt = l.GitBranch, rec.at
			}
			for _, k := range models.IssueKeysFromBranch(l.GitBranch) {
				if !seenKey[k] {
					seenKey[k] = true
					s.IssueKeys = append(s.IssueKeys, k)
				}
			}
		}
		if l.Type != "assistant" || l.Message == nil || l.Message.Model == "" || strings.HasPrefix(l.Message.Model, "<") {
			continue // <synthetic> lines are not model calls
		}
		id := l.Message.ID
		if id == "" {
			anon++
			id = fmt.Sprintf("\x00anon-%d", anon)
		}
		m := msg{model: l.Message.Model}
		if u := l.Message.Usage; u != nil {
			m.u = models.SessionUsage{Model: l.Message.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens}
		}
		if _, seen := byID[id]; !seen {
			order = append(order, id)
		}
		byID[id] = m // the last line of a message carries its final usage
	}
	if len(order) == 0 {
		return s, false, nil
	}
	s.Messages = len(order)
	sums := map[string]*models.SessionUsage{}
	var modelsSeen []string
	for _, id := range order {
		m := byID[id]
		u, ok := sums[m.model]
		if !ok {
			u = &models.SessionUsage{Model: m.model}
			sums[m.model] = u
			modelsSeen = append(modelsSeen, m.model)
		}
		u.InputTokens += m.u.InputTokens
		u.OutputTokens += m.u.OutputTokens
		u.CacheReadTokens += m.u.CacheReadTokens
		u.CacheWriteTokens += m.u.CacheWriteTokens
	}
	for _, name := range modelsSeen {
		s.Usage = append(s.Usage, *sums[name])
	}
	if s.EndedAt.Before(s.StartedAt) || s.StartedAt.IsZero() {
		return s, false, nil // no usable timestamps
	}
	return s, true, nil
}

// sessionPayload is the PUT body: the only fields that leave the machine.
type sessionPayload struct {
	ProjectPath string                `json:"projectPath"`
	GitBranch   string                `json:"gitBranch"`
	IssueKeys   []string              `json:"issueKeys"`
	StartedAt   time.Time             `json:"startedAt"`
	EndedAt     time.Time             `json:"endedAt"`
	Messages    int                   `json:"messages"`
	Usage       []sessionUsagePayload `json:"usage"`
}

type sessionUsagePayload struct {
	Model            string `json:"model"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
}

func payloadFor(s models.AgentSession) sessionPayload {
	p := sessionPayload{ProjectPath: s.ProjectPath, GitBranch: s.GitBranch, IssueKeys: s.IssueKeys,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, Messages: s.Messages, Usage: []sessionUsagePayload{}}
	if p.IssueKeys == nil {
		p.IssueKeys = []string{}
	}
	for _, u := range s.Usage {
		p.Usage = append(p.Usage, sessionUsagePayload{Model: u.Model, InputTokens: u.InputTokens,
			OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens})
	}
	return p
}

type pushClient struct {
	base, token, workspace string
	http                   *http.Client
}

func (c pushClient) put(ctx context.Context, s models.AgentSession) (models.AgentSession, error) {
	body, err := json.Marshal(payloadFor(s))
	if err != nil {
		return models.AgentSession{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.base+"/api/sessions/"+url.PathEscape(s.ExternalID), bytes.NewReader(body))
	if err != nil {
		return models.AgentSession{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.workspace != "" {
		req.Header.Set("X-Workspace", c.workspace)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return models.AgentSession{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return models.AgentSession{}, fmt.Errorf("server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var saved models.AgentSession
	if err := json.Unmarshal(raw, &saved); err != nil {
		return models.AgentSession{}, fmt.Errorf("bad response: %w", err)
	}
	return saved, nil
}

// transcript is one main transcript file and its subagent files.
type transcript struct {
	main      string
	subagents []string
}

func (t transcript) stem() string { return strings.TrimSuffix(filepath.Base(t.main), ".jsonl") }

func (t transcript) lastChanged() time.Time {
	var last time.Time
	for _, p := range append([]string{t.main}, t.subagents...) {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return last
}

func transcriptAt(path string) transcript {
	subs, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "*.jsonl"))
	sort.Strings(subs)
	return transcript{main: path, subagents: subs}
}

// findTranscripts walks dir for main transcripts; files under a subagents
// folder belong to their parent session and are read with it.
func findTranscripts(dir string) ([]transcript, error) {
	var out []transcript
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".jsonl") {
			out = append(out, transcriptAt(p))
		}
		return nil
	})
	return out, err
}

func readTranscript(t transcript) (models.AgentSession, bool, error) {
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	var readers []io.Reader
	for _, p := range append([]string{t.main}, t.subagents...) {
		f, err := os.Open(p)
		if err != nil {
			return models.AgentSession{}, false, err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	return parseTranscript(t.stem(), readers[0], readers[1:]...)
}

func readTokenFile(env func(string) string) string {
	home := env("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".config", "donewhen", "token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// runSessions runs `donewhen sessions …` and returns the exit code: 0 when
// every session was sent, 1 when any failed, 2 for bad usage.
func runSessions(args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "push" {
		fmt.Fprint(stderr, sessionsUsage)
		return 2
	}
	fl := flag.NewFlagSet("sessions push", flag.ContinueOnError)
	fl.SetOutput(stderr)
	file := fl.String("file", "", "one transcript (.jsonl)")
	dir := fl.String("dir", "", "a folder of transcripts, e.g. ~/.claude/projects")
	since := fl.String("since", "", "with --dir: skip files last changed before this date (YYYY-MM-DD)")
	if err := fl.Parse(args[1:]); err != nil || fl.NArg() > 0 || (*file == "") == (*dir == "") {
		fmt.Fprint(stderr, sessionsUsage)
		return 2
	}
	var sinceT time.Time
	if *since != "" {
		t, err := time.ParseInLocation(time.DateOnly, *since, time.Local)
		if err != nil {
			fmt.Fprintf(stderr, "--since must be YYYY-MM-DD\n")
			return 2
		}
		sinceT = t
	}

	base := strings.TrimRight(env("DONEWHEN_URL"), "/")
	if base == "" {
		fmt.Fprintln(stderr, "DONEWHEN_URL is not set")
		return 1
	}
	token := env("DONEWHEN_TOKEN")
	if token == "" {
		token = readTokenFile(env)
	}
	if token == "" {
		fmt.Fprintln(stderr, "no token: set DONEWHEN_TOKEN or write ~/.config/donewhen/token")
		return 1
	}
	client := pushClient{base: base, token: token, workspace: env("DONEWHEN_WORKSPACE"),
		http: &http.Client{Timeout: 30 * time.Second}}

	var list []transcript
	if *file != "" {
		list = []transcript{transcriptAt(*file)}
	} else {
		found, err := findTranscripts(*dir)
		if err != nil {
			fmt.Fprintf(stderr, "read %s: %v\n", *dir, err)
			return 1
		}
		for _, t := range found {
			if sinceT.IsZero() || !t.lastChanged().Before(sinceT) {
				list = append(list, t)
			}
		}
	}

	failed := false
	ctx := context.Background()
	for _, t := range list {
		s, ok, err := readTranscript(t)
		if err != nil {
			fmt.Fprintf(stderr, "failed %s: %v\n", t.main, err)
			failed = true
			continue
		}
		if !ok {
			continue // nothing a model said: nothing to record
		}
		saved, err := client.put(ctx, s)
		if err != nil {
			fmt.Fprintf(stderr, "failed %s: %v\n", s.ExternalID, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "sent %s %d $%.4f\n", saved.ExternalID, saved.Tokens, saved.CostUSD)
	}
	if failed {
		return 1
	}
	return 0
}
