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
// each session as PUT /api/sessions/{id}, in chunks of messages. It sends
// numbers and metadata only: the parser decodes nothing but the fields of
// transcriptLine (ids, cwd, branch, time, model, usage), so message text, tool
// input and tool output never reach the request.

const sessionsUsage = `usage:
  donewhen sessions push --file <transcript.jsonl>
  donewhen sessions push --dir <dir> [--since YYYY-MM-DD]

env: DONEWHEN_URL, DONEWHEN_TOKEN (or ~/.config/donewhen/token), DONEWHEN_WORKSPACE
`

// transcriptLine is everything the importer reads from one jsonl line.
type transcriptLine struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Timestamp string `json:"timestamp"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64          `json:"input_tokens"`
			OutputTokens             int64          `json:"output_tokens"`
			CacheReadInputTokens     int64          `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64          `json:"cache_creation_input_tokens"`
			CacheCreation            *cacheCreation `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// cacheCreation is the split of a message's cache writes by cache lifetime.
type cacheCreation struct {
	Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
}

type record struct {
	line transcriptLine
	at   time.Time
	ok   bool // at was parsed
	any  bool // belongs to the session whatever its sessionId (subagent file)
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

// cacheWrites splits a message's cache writes into the 5-minute and 1-hour
// parts. Without the split, the whole write counts as 5-minute.
func cacheWrites(total int64, split *cacheCreation) (w5, w1 int64) {
	if split == nil || split.Ephemeral5m+split.Ephemeral1h == 0 {
		return total, 0
	}
	return split.Ephemeral5m, split.Ephemeral1h
}

// parseTranscript reads one session: its main transcript and the transcripts
// of its subagents. The session id is the file name stem; when no line of the
// main transcript has that id, it is the first session in it. Main transcript
// lines of other sessions are ignored; every subagent line belongs to the
// session. With whole set, every line of main belongs to the session too (a
// subagent file pushed on its own, attributed to its parent session).
//
// It returns the session's metadata and one entry per message id: Claude Code
// writes one line per content block of the same message, so usage is taken
// once, from the last line of the message. ok is false when no model message
// was found.
func parseTranscript(stem string, whole bool, main io.Reader, subagents ...io.Reader) (models.AgentSession, []models.SessionMessage, bool, error) {
	var recs []record
	for i, r := range append([]io.Reader{main}, subagents...) {
		got, err := readRecords(r)
		if err != nil {
			return models.AgentSession{}, nil, false, err
		}
		if i > 0 || whole {
			for j := range got {
				got[j].any = true
			}
		}
		if i == 0 && !whole {
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
	byID := map[string]*models.SessionMessage{}
	var order []string
	var lastBranchAt time.Time
	seenKey := map[string]bool{}
	for _, rec := range recs {
		l := rec.line
		if !rec.any && l.SessionID != stem {
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
			if l.UUID == "" {
				continue // no stable id: it could not be counted once
			}
			id = "line:" + l.UUID
		}
		m, seen := byID[id]
		if !seen {
			m = &models.SessionMessage{ID: id}
			if rec.ok {
				m.At = rec.at
			}
			byID[id] = m
			order = append(order, id)
		}
		m.Model = l.Message.Model
		if u := l.Message.Usage; u != nil { // the last line of a message carries its final usage
			m.InputTokens, m.OutputTokens, m.CacheReadTokens = u.InputTokens, u.OutputTokens, u.CacheReadInputTokens
			m.CacheWrite5mTokens, m.CacheWrite1hTokens = cacheWrites(u.CacheCreationInputTokens, u.CacheCreation)
		}
	}
	if len(order) == 0 || s.StartedAt.IsZero() {
		return s, nil, false, nil
	}
	msgs := make([]models.SessionMessage, 0, len(order))
	for _, id := range order {
		msgs = append(msgs, *byID[id])
	}
	return s, msgs, true, nil
}

// sessionPayload is the PUT body: the only fields that leave the machine.
type sessionPayload struct {
	ProjectPath string           `json:"projectPath"`
	GitBranch   string           `json:"gitBranch"`
	IssueKeys   []string         `json:"issueKeys"`
	StartedAt   time.Time        `json:"startedAt"`
	EndedAt     time.Time        `json:"endedAt"`
	Messages    []messagePayload `json:"messages"`
}

// messagePayload is one message's usage. id is Claude's opaque message id.
type messagePayload struct {
	ID                 string     `json:"id"`
	Model              string     `json:"model"`
	At                 *time.Time `json:"at,omitempty"`
	InputTokens        int64      `json:"inputTokens"`
	OutputTokens       int64      `json:"outputTokens"`
	CacheReadTokens    int64      `json:"cacheReadTokens"`
	CacheWrite5mTokens int64      `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64      `json:"cacheWrite1hTokens"`
}

func payloadFor(s models.AgentSession, msgs []models.SessionMessage) sessionPayload {
	p := sessionPayload{ProjectPath: s.ProjectPath, GitBranch: s.GitBranch, IssueKeys: s.IssueKeys,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, Messages: []messagePayload{}}
	if p.IssueKeys == nil {
		p.IssueKeys = []string{}
	}
	for _, m := range msgs {
		mp := messagePayload{ID: m.ID, Model: m.Model, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadTokens, CacheWrite5mTokens: m.CacheWrite5mTokens, CacheWrite1hTokens: m.CacheWrite1hTokens}
		if !m.At.IsZero() {
			at := m.At
			mp.At = &at
		}
		p.Messages = append(p.Messages, mp)
	}
	return p
}

// pushChunkSize is how many messages one PUT carries, so a long session stays
// under the server's body size limit. A variable so tests can make it small.
var pushChunkSize = 2000

type pushClient struct {
	base, token, workspace string
	http                   *http.Client
}

// push sends a session in chunks of pushChunkSize messages and returns the
// server's view after the last one. A PUT is additive on the server, so the
// chunks add up to one push.
func (c pushClient) push(ctx context.Context, s models.AgentSession, msgs []models.SessionMessage) (models.AgentSession, error) {
	var saved models.AgentSession
	for start := 0; start < len(msgs); start += pushChunkSize {
		end := min(start+pushChunkSize, len(msgs))
		var err error
		if saved, err = c.put(ctx, s, msgs[start:end]); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

func (c pushClient) put(ctx context.Context, s models.AgentSession, msgs []models.SessionMessage) (models.AgentSession, error) {
	body, err := json.Marshal(payloadFor(s, msgs))
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

// transcript is one session's files: a main transcript and its subagent
// transcripts, or a single subagent file pushed on its own.
type transcript struct {
	main      string
	subagents []string
	session   string // the session id the files belong to
	whole     bool   // main is a subagent file: all its lines belong to session
}

func (t transcript) lastChanged() time.Time {
	var last time.Time
	for _, p := range append([]string{t.main}, t.subagents...) {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return last
}

// subagentFiles lists the subagent transcripts of a session, at any depth
// under <session>/subagents (also workflows/wf_*/). Only agent-*.jsonl files
// are transcripts; journal.jsonl and other files are not.
func subagentFiles(mainPath string) []string {
	root := filepath.Join(strings.TrimSuffix(mainPath, ".jsonl"), "subagents")
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a missing or unreadable folder: no subagents
		}
		name := d.Name()
		if !d.IsDir() && isSubagentTranscript(name) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// isSubagentTranscript reports whether a file under subagents/ is an agent
// transcript; journal.jsonl and other files there are not.
func isSubagentTranscript(path string) bool {
	name := filepath.Base(path)
	return strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl")
}

// transcriptAt reads a path given with --file. A file under
// <session>/subagents/ is a subagent transcript: it is sent on its own,
// attributed to its parent session.
func transcriptAt(path string) transcript {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := len(parts) - 2; i >= 1; i-- {
		if parts[i] == "subagents" {
			return transcript{main: path, session: parts[i-1], whole: true}
		}
	}
	return transcript{main: path, subagents: subagentFiles(path),
		session: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
}

// findTranscripts walks dir for main transcripts, oldest first, so a session
// that was resumed later never takes the messages its first session sent.
// Files under a subagents folder belong to their parent session.
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
	changed := make(map[string]time.Time, len(out))
	for _, t := range out {
		changed[t.main] = t.lastChanged()
	}
	sort.SliceStable(out, func(i, j int) bool { return changed[out[i].main].Before(changed[out[j].main]) })
	return out, err
}

func readTranscript(t transcript) (models.AgentSession, []models.SessionMessage, bool, error) {
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
			return models.AgentSession{}, nil, false, err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	return parseTranscript(t.session, t.whole, readers[0], readers[1:]...)
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
		t := transcriptAt(*file)
		if t.whole && !isSubagentTranscript(*file) {
			fmt.Fprintf(stderr, "%s is not a transcript: under subagents/ only agent-*.jsonl files are (journal.jsonl is not)\n", *file)
			return 2
		}
		list = []transcript{t}
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
		s, msgs, ok, err := readTranscript(t)
		if err != nil {
			fmt.Fprintf(stderr, "failed %s: %v\n", t.main, err)
			failed = true
			continue
		}
		if !ok {
			continue // nothing a model said: nothing to record
		}
		saved, err := client.push(ctx, s, msgs)
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
