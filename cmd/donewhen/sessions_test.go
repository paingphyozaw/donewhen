package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnreginald/donewhen/internal/api"
	"github.com/johnreginald/donewhen/internal/auth"
	"github.com/johnreginald/donewhen/internal/config"
	"github.com/johnreginald/donewhen/internal/db"
	"github.com/johnreginald/donewhen/internal/events"
	"github.com/johnreginald/donewhen/internal/models"
	"github.com/johnreginald/donewhen/internal/service"
	"github.com/johnreginald/donewhen/internal/store"
	"github.com/johnreginald/donewhen/internal/testdb"
)

// BIZ-278: `donewhen sessions push`. Fixtures are written by hand: no real
// transcript content is ever committed.

const sid = "11111111-2222-3333-4444-555555555555"

// Text that must never leave the machine.
var secrets = []string{"SECRET-PROMPT-TEXT", "SECRET-ANSWER-TEXT", "SECRET-TOOL-INPUT", "SECRET-TOOL-OUTPUT", "SECRET-THINKING"}

func line(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// usage is one message's usage as Claude Code writes it. split nil leaves
// out usage.cache_creation (older transcripts).
func usage(in, out, cr, cw int64, split []int64) map[string]any {
	u := map[string]any{"input_tokens": in, "output_tokens": out,
		"cache_read_input_tokens": cr, "cache_creation_input_tokens": cw}
	if split != nil {
		u["cache_creation"] = map[string]any{"ephemeral_5m_input_tokens": split[0], "ephemeral_1h_input_tokens": split[1]}
	}
	return u
}

func assistant(session, branch, ts, id, model string, u map[string]any, content any) map[string]any {
	return map[string]any{
		"type": "assistant", "sessionId": session, "cwd": "/work/app", "gitBranch": branch, "timestamp": ts,
		"uuid":    "line-" + id + "-" + ts,
		"message": map[string]any{"id": id, "model": model, "role": "assistant", "content": content, "usage": u},
	}
}

var (
	textBlock  = []map[string]any{{"type": "text", "text": "SECRET-ANSWER-TEXT"}}
	toolBlock  = []map[string]any{{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "SECRET-TOOL-INPUT"}}}
	thinkBlock = []map[string]any{{"type": "thinking", "thinking": "SECRET-THINKING"}}
)

// mainTranscript has two models, one message split over two lines (one per
// content block), a synthetic line, a foreign session line and lines full of
// text that must not be sent.
func mainTranscript(t *testing.T, session string) string {
	var b strings.Builder
	b.WriteString(line(t, map[string]any{"type": "user", "sessionId": session, "cwd": "/work/app", "gitBranch": "feature/biz-277-a",
		"timestamp": "2026-11-01T10:00:00.000Z", "message": map[string]any{"role": "user", "content": "SECRET-PROMPT-TEXT"}}))
	b.WriteString(line(t, assistant(session, "feature/biz-277-a", "2026-11-01T10:00:05.000Z", "msg_1", "claude-opus-5-5",
		usage(10, 20, 100, 1000, []int64{0, 1000}), thinkBlock)))
	b.WriteString(line(t, assistant(session, "feature/biz-277-a", "2026-11-01T10:00:06.000Z", "msg_1", "claude-opus-5-5",
		usage(10, 20, 100, 1000, []int64{0, 1000}), toolBlock)))
	b.WriteString(line(t, map[string]any{"type": "user", "sessionId": session, "cwd": "/work/app", "gitBranch": "feature/biz-277-a",
		"timestamp": "2026-11-01T10:00:07.000Z", "message": map[string]any{"role": "user",
			"content": []map[string]any{{"type": "tool_result", "content": "SECRET-TOOL-OUTPUT"}}},
		"toolUseResult": map[string]any{"stdout": "SECRET-TOOL-OUTPUT"}}))
	b.WriteString(line(t, assistant(session, "feature/biz-278-b", "2026-11-01T10:30:00.000Z", "msg_2", "claude-opus-5-5",
		usage(1, 2, 3, 4, nil), textBlock)))
	b.WriteString(line(t, assistant(session, "feature/biz-278-b", "2026-11-01T10:40:00.000Z", "msg_3", "claude-sonnet-5-5",
		usage(5, 6, 7, 8, []int64{8, 0}), textBlock)))
	b.WriteString(line(t, assistant(session, "", "2026-11-01T10:41:00.000Z", "msg_s", "<synthetic>", usage(0, 0, 0, 0, nil), textBlock)))
	b.WriteString(line(t, assistant("other-session", "main", "2026-10-01T00:00:00.000Z", "msg_x", "claude-opus-5-5",
		usage(999, 999, 999, 999, nil), textBlock)))
	b.WriteString("this line is not json\n")
	b.WriteString(line(t, map[string]any{"type": "summary", "summary": "SECRET-PROMPT-TEXT"}))
	return b.String()
}

func subagentTranscript(t *testing.T, id string, n int64, ts string) string {
	return line(t, assistant(sid, "feature/biz-278-b", ts, id, "claude-haiku-5-5", usage(n, n, n, n, []int64{0, n}), textBlock))
}

func TestParseTranscriptPerMessage(t *testing.T) {
	s, msgs, ok, err := parseTranscript(sid, false, strings.NewReader(mainTranscript(t, sid)),
		strings.NewReader(subagentTranscript(t, "msg_4", 1, "2026-11-01T10:50:00.000Z")))
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if s.ExternalID != sid || s.ProjectPath != "/work/app" || s.GitBranch != "feature/biz-278-b" {
		t.Fatalf("meta = %+v", s)
	}
	if strings.Join(s.IssueKeys, ",") != "BIZ-277,BIZ-278" {
		t.Fatalf("issue keys = %v", s.IssueKeys)
	}
	if !s.StartedAt.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) ||
		!s.EndedAt.Equal(time.Date(2026, 11, 1, 10, 50, 0, 0, time.UTC)) {
		t.Fatalf("times = %v .. %v", s.StartedAt, s.EndedAt)
	}
	at := func(v string) time.Time { tm, _ := time.Parse(time.RFC3339, v); return tm }
	want := []models.SessionMessage{
		// msg_1 is on two lines: counted once, at its first line.
		{ID: "msg_1", Model: "claude-opus-5-5", At: at("2026-11-01T10:00:05Z"), InputTokens: 10, OutputTokens: 20, CacheReadTokens: 100, CacheWrite1hTokens: 1000},
		// No cache_creation split: the write counts as 5-minute.
		{ID: "msg_2", Model: "claude-opus-5-5", At: at("2026-11-01T10:30:00Z"), InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWrite5mTokens: 4},
		{ID: "msg_3", Model: "claude-sonnet-5-5", At: at("2026-11-01T10:40:00Z"), InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWrite5mTokens: 8},
		{ID: "msg_4", Model: "claude-haiku-5-5", At: at("2026-11-01T10:50:00Z"), InputTokens: 1, OutputTokens: 1, CacheReadTokens: 1, CacheWrite1hTokens: 1},
	}
	if len(msgs) != len(want) {
		t.Fatalf("messages = %+v", msgs)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Errorf("message %d = %+v, want %+v", i, msgs[i], want[i])
		}
	}
}

func TestParseTranscriptFallsBackToFirstSession(t *testing.T) {
	// A file whose name is not its session id: the first session in it wins.
	s, _, ok, err := parseTranscript("not-the-id", false, strings.NewReader(mainTranscript(t, sid)))
	if err != nil || !ok || s.ExternalID != sid {
		t.Fatalf("got %q ok=%v err=%v", s.ExternalID, ok, err)
	}
	// A subagent file on its own belongs to the session it is filed under.
	s, msgs, ok, err := parseTranscript("parent-id", true, strings.NewReader(subagentTranscript(t, "msg_4", 1, "2026-11-01T10:50:00Z")))
	if err != nil || !ok || s.ExternalID != "parent-id" || len(msgs) != 1 {
		t.Fatalf("subagent alone: %q %d ok=%v err=%v", s.ExternalID, len(msgs), ok, err)
	}
	// Nothing a model said → nothing to send.
	_, _, ok, err = parseTranscript("x", false, strings.NewReader(`{"type":"user","sessionId":"x","timestamp":"2026-11-01T10:00:00Z"}`+"\n"))
	if err != nil || ok {
		t.Fatalf("empty session: ok=%v err=%v", ok, err)
	}
}

// fakeServer records each PUT and answers with the sums of that request.
type fakeServer struct {
	mu     sync.Mutex
	bodies map[string][]string // session id → bodies, in order
	auth   []string
	wsHdr  []string
}

func (f *fakeServer) messages(id string) []messagePayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []messagePayload
	for _, b := range f.bodies[id] {
		var p sessionPayload
		_ = json.Unmarshal([]byte(b), &p)
		out = append(out, p.Messages...)
	}
	return out
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	f := &fakeServer{bodies: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		if r.Method != http.MethodPut || id == r.URL.Path {
			http.Error(w, "bad route", 404)
			return
		}
		f.mu.Lock()
		f.bodies[id] = append(f.bodies[id], string(raw))
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.wsHdr = append(f.wsHdr, r.Header.Get("X-Workspace"))
		f.mu.Unlock()
		var p sessionPayload
		_ = json.Unmarshal(raw, &p)
		out := models.AgentSession{ExternalID: id, CostUSD: 1.5}
		for _, m := range p.Messages {
			out.Tokens += m.InputTokens + m.OutputTokens + m.CacheReadTokens + m.CacheWrite5mTokens + m.CacheWrite1hTokens
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeTranscript lays out one session as Claude Code does: the main file, a
// subagent, a workflow subagent and a workflow journal (not a transcript).
func writeTranscript(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, sid+".jsonl")
	write(t, p, mainTranscript(t, sid))
	write(t, filepath.Join(dir, sid, "subagents", "agent-a1.jsonl"), subagentTranscript(t, "msg_4", 1, "2026-11-01T10:50:00Z"))
	write(t, filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "agent-b2.jsonl"),
		subagentTranscript(t, "msg_5", 2, "2026-11-01T10:55:00Z"))
	write(t, filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "journal.jsonl"),
		subagentTranscript(t, "msg_journal", 999, "2026-11-01T10:56:00Z"))
	return p
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func ids(msgs []messagePayload) string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestPushSendsNoText(t *testing.T) {
	f, srv := newFakeServer(t)
	path := writeTranscript(t, t.TempDir())
	var out, errOut bytes.Buffer
	code := runSessions([]string{"push", "--file", path},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL + "/", "DONEWHEN_TOKEN": "dw_test", "DONEWHEN_WORKSPACE": "biz"}),
		&out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(f.bodies) != 1 || len(f.bodies[sid]) != 1 {
		t.Fatalf("bodies = %v", f.bodies)
	}
	body := f.bodies[sid][0]
	for _, s := range secrets {
		if strings.Contains(body, s) {
			t.Fatalf("request body leaks %q: %s", s, body)
		}
	}
	// Only the allowed fields, nothing else.
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "endedAt,gitBranch,issueKeys,messages,projectPath,startedAt" {
		t.Fatalf("body fields = %v", keys)
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(top["messages"], &msgs)
	for _, m := range msgs {
		var mk []string
		for k := range m {
			mk = append(mk, k)
		}
		sort.Strings(mk)
		if strings.Join(mk, ",") != "at,cacheReadTokens,cacheWrite1hTokens,cacheWrite5mTokens,id,inputTokens,model,outputTokens" {
			t.Fatalf("message fields = %v", mk)
		}
	}
	// Subagents at any depth are part of the session; the journal is not.
	if got := ids(f.messages(sid)); got != "msg_1,msg_2,msg_3,msg_4,msg_5" {
		t.Fatalf("messages sent = %s", got)
	}
	if f.auth[0] != "Bearer dw_test" || f.wsHdr[0] != "biz" {
		t.Fatalf("headers: auth %q ws %q", f.auth[0], f.wsHdr[0])
	}
	// 1130 + 10 + 26 + 4 + 8 = 1178 tokens.
	if want := "sent " + sid + " 1178 $1.5000\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

func TestPushChunks(t *testing.T) {
	f, srv := newFakeServer(t)
	path := writeTranscript(t, t.TempDir())
	old := pushChunkSize
	pushChunkSize = 2
	t.Cleanup(func() { pushChunkSize = old })
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"push", "--file", path},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "t"}), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if n := len(f.bodies[sid]); n != 3 { // 5 messages in chunks of 2
		t.Fatalf("%d requests", n)
	}
	if got := ids(f.messages(sid)); got != "msg_1,msg_2,msg_3,msg_4,msg_5" {
		t.Fatalf("messages sent = %s", got)
	}
}

func TestPushSubagentFileAlone(t *testing.T) {
	f, srv := newFakeServer(t)
	dir := t.TempDir()
	writeTranscript(t, dir)
	var out, errOut bytes.Buffer
	sub := filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "agent-b2.jsonl")
	if code := runSessions([]string{"push", "--file", sub},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "t"}), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got := ids(f.messages(sid)); len(f.bodies) != 1 || got != "msg_5" {
		t.Fatalf("sent %v / %s", f.bodies, got)
	}
	// A workflow journal is not a transcript: refused, nothing sent.
	errOut.Reset()
	journal := filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "journal.jsonl")
	if code := runSessions([]string{"push", "--file", journal},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "t"}), &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), "not a transcript") {
		t.Fatalf("journal: exit %d, stderr %q", code, errOut.String())
	}
	if len(f.bodies[sid]) != 1 {
		t.Fatalf("journal push sent %d bodies", len(f.bodies[sid]))
	}
}

func TestPushDirSince(t *testing.T) {
	f, srv := newFakeServer(t)
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "proj-a"))
	const oldID = "22222222-0000-0000-0000-000000000000"
	old := filepath.Join(dir, "proj-b", oldID+".jsonl")
	write(t, old, mainTranscript(t, oldID))
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	env := envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "dw_test"})
	if code := runSessions([]string{"push", "--dir", dir, "--since", "2026-06-01"}, env, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	// Subagent files are part of their session, never a session of their own.
	if len(f.bodies) != 1 || f.bodies[sid] == nil {
		t.Fatalf("sent %d sessions: %v", len(f.bodies), f.bodies)
	}
	// Without --since both go, the older file first.
	f.bodies = map[string][]string{}
	out.Reset()
	if code := runSessions([]string{"push", "--dir", dir}, env, &out, &errOut); code != 0 || len(f.bodies) != 2 {
		t.Fatalf("exit %d, sent %d", code, len(f.bodies))
	}
	if !strings.HasPrefix(out.String(), "sent "+oldID) {
		t.Fatalf("older session not first: %q", out.String())
	}
}

func TestPushServerDown(t *testing.T) {
	path := writeTranscript(t, t.TempDir())
	var out, errOut bytes.Buffer
	code := runSessions([]string{"push", "--file", path},
		envOf(map[string]string{"DONEWHEN_URL": "http://127.0.0.1:1", "DONEWHEN_TOKEN": "dw_test"}), &out, &errOut)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (stderr %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "failed "+sid) {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

// realServer runs the DoneWhen API on the throwaway test database and returns
// its URL and a token pinned to a fresh workspace.
func realServer(t *testing.T) (string, string, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := store.New(pool, "K")
	n := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	u, err := st.CreateUser(ctx, "push"+n+"@example.test", "x")
	if err != nil {
		t.Fatal(err)
	}
	w, err := st.CreateWorkspace(ctx, "Push "+n, "push-"+n, "P"+n[:5], u.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspaces WHERE id=$1`, w.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, u.ID)
	})
	raw, _ := auth.RandomToken(32)
	tok := auth.TokenPrefix + raw
	if _, err := st.CreateAPIToken(ctx, u.ID, "push", auth.HashToken(tok), &w.ID); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	srv := api.NewServer(config.Config{}, st, service.New(st, bus), bus, nil)
	t.Cleanup(srv.Close)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs.URL, tok, st, w.ID
}

func modelTotals(t *testing.T, st *store.Store, wsID string) map[string]models.SessionSummaryRow {
	t.Helper()
	rows, err := st.SessionSummary(context.Background(), wsID, store.SessionFilter{}, store.SessionGroupModel)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]models.SessionSummaryRow{}
	for _, r := range rows {
		out[r.Key] = r
	}
	return out
}

// End to end against the real API: a resumed session repeats the first
// session's messages, the same push is sent twice, a subagent file is sent on
// its own, and a chunked push gives the same totals.
func TestPushAgainstServer(t *testing.T) {
	url, tok, st, wsID := realServer(t)
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "proj"))
	// The resumed session copies the first session's lines under its own id,
	// then adds one new message.
	const resumed = "33333333-0000-0000-0000-000000000000"
	copied := strings.ReplaceAll(mainTranscript(t, sid), `"sessionId":"`+sid+`"`, `"sessionId":"`+resumed+`"`) +
		line(t, assistant(resumed, "feature/biz-279-c", "2026-11-02T09:00:00.000Z", "msg_new", "claude-opus-5-5",
			usage(100, 0, 0, 0, nil), textBlock))
	resumedPath := filepath.Join(dir, "proj", resumed+".jsonl")
	write(t, resumedPath, copied)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(resumedPath, later, later); err != nil {
		t.Fatal(err)
	}
	env := envOf(map[string]string{"DONEWHEN_URL": url, "DONEWHEN_TOKEN": tok})
	push := func(args ...string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := runSessions(append([]string{"push"}, args...), env, &out, &errOut); code != 0 {
			t.Fatalf("push %v: exit %d: %s", args, code, errOut.String())
		}
	}
	// Opus: msg_1 1130 + msg_2 10 + msg_new 100; sonnet 26; haiku msg_4 4 + msg_5 8.
	want := map[string]int64{"claude-opus-5-5": 1240, "claude-sonnet-5-5": 26, "claude-haiku-5-5": 12}
	check := func(when string) {
		t.Helper()
		got := modelTotals(t, st, wsID)
		for m, n := range want {
			if got[m].Tokens != n {
				t.Fatalf("%s: %s tokens = %d, want %d (%+v)", when, m, got[m].Tokens, n, got)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: models = %+v", when, got)
		}
		r, err := st.GetSession(context.Background(), wsID, resumed)
		if err != nil || r.Tokens != 100 || r.Messages != 1 {
			t.Fatalf("%s: resumed session = %+v (%v)", when, r, err)
		}
	}
	push("--dir", dir)
	check("first push")
	push("--dir", dir)
	check("same push again")
	push("--file", filepath.Join(dir, "proj", sid, "subagents", "agent-a1.jsonl"))
	check("subagent file alone")
	first, err := st.GetSession(context.Background(), wsID, sid)
	if err != nil || first.Messages != 5 || first.Tokens != 1178 {
		t.Fatalf("first session = %+v (%v)", first, err)
	}

	// Chunked into one message per request: the same totals in a fresh workspace.
	url2, tok2, st2, ws2 := realServer(t)
	old := pushChunkSize
	pushChunkSize = 1
	t.Cleanup(func() { pushChunkSize = old })
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"push", "--dir", dir}, envOf(map[string]string{"DONEWHEN_URL": url2, "DONEWHEN_TOKEN": tok2}),
		&out, &errOut); code != 0 {
		t.Fatalf("chunked push: exit %d: %s", code, errOut.String())
	}
	got := modelTotals(t, st2, ws2)
	for m, n := range want {
		if got[m].Tokens != n {
			t.Fatalf("chunked: %s tokens = %d, want %d", m, got[m].Tokens, n)
		}
	}
	c, err := st2.GetSession(context.Background(), ws2, sid)
	if err != nil || c.Messages != first.Messages || c.Tokens != first.Tokens || !c.StartedAt.Equal(first.StartedAt) ||
		!c.EndedAt.Equal(first.EndedAt) || c.CostUSD-first.CostUSD > 1e-6 || first.CostUSD-c.CostUSD > 1e-6 {
		t.Fatalf("chunked %+v\nsingle %+v", c, first)
	}
}

func TestPushUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	env := envOf(map[string]string{"DONEWHEN_URL": "http://127.0.0.1:1", "DONEWHEN_TOKEN": "t"})
	for _, args := range [][]string{
		{},
		{"pull"},
		{"push"},
		{"push", "--file", "a", "--dir", "b"},
		{"push", "--dir", "b", "--since", "yesterday"},
	} {
		if code := runSessions(args, env, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	// No URL → cannot push.
	if code := runSessions([]string{"push", "--file", "x.jsonl"}, envOf(nil), &out, &errOut); code != 1 {
		t.Errorf("no url: exit %d", code)
	}
}

func TestSessionEndHookAlwaysExitsZero(t *testing.T) {
	script, err := filepath.Abs("../../scripts/claude-session-end.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "args")
	stub := filepath.Join(dir, "donewhen-stub")
	// A donewhen that records its arguments and then fails, as when the server is down.
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" > '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, env ...string) {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), env...)
		if err := cmd.Run(); err != nil {
			t.Fatalf("hook with %q failed: %v", stdin, err)
		}
	}
	transcript := filepath.Join(dir, "my session.jsonl")
	run(`{"session_id":"abc","transcript_path":"`+transcript+`","hook_event_name":"SessionEnd"}`,
		"DONEWHEN_BIN="+stub, "DONEWHEN_SESSIONS_LOG="+filepath.Join(dir, "log"))
	// The push runs in the background; give a loaded machine time to start it.
	deadline := time.Now().Add(20 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if got, _ = os.ReadFile(marker); len(got) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.TrimSpace(string(got)) != "sessions push --file "+transcript {
		logged, _ := os.ReadFile(filepath.Join(dir, "log"))
		t.Fatalf("donewhen called with %q (log %q)", got, logged)
	}
	// Bad input, a missing binary: still exit 0.
	run("not json", "DONEWHEN_BIN="+stub)
	run("", "DONEWHEN_BIN="+stub)
	run(`{"transcript_path":"/x.jsonl"}`, "DONEWHEN_BIN="+filepath.Join(dir, "missing"))
}
