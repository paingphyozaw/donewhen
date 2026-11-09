package main

import (
	"bytes"
	"encoding/json"
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

	"github.com/johnreginald/donewhen/internal/models"
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

func assistant(session, branch, ts, id, model string, in, out, cr, cw int64, content any) map[string]any {
	return map[string]any{
		"type": "assistant", "sessionId": session, "cwd": "/work/app", "gitBranch": branch, "timestamp": ts,
		"message": map[string]any{
			"id": id, "model": model, "role": "assistant", "content": content,
			"usage": map[string]any{"input_tokens": in, "output_tokens": out,
				"cache_read_input_tokens": cr, "cache_creation_input_tokens": cw,
				"cache_creation": map[string]any{"ephemeral_1h_input_tokens": cw}},
		},
	}
}

// mainTranscript has two models, one message split over two lines (one per
// content block), a synthetic line, a foreign session line and lines full of
// text that must not be sent.
func mainTranscript(t *testing.T) string {
	text := []map[string]any{{"type": "text", "text": "SECRET-ANSWER-TEXT"}}
	tool := []map[string]any{{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "SECRET-TOOL-INPUT"}}}
	think := []map[string]any{{"type": "thinking", "thinking": "SECRET-THINKING"}}
	var b strings.Builder
	b.WriteString(line(t, map[string]any{"type": "user", "sessionId": sid, "cwd": "/work/app", "gitBranch": "feature/biz-277-a",
		"timestamp": "2026-11-01T10:00:00.000Z", "message": map[string]any{"role": "user", "content": "SECRET-PROMPT-TEXT"}}))
	b.WriteString(line(t, assistant(sid, "feature/biz-277-a", "2026-11-01T10:00:05.000Z", "msg_1", "claude-opus-5-5", 10, 20, 100, 1000, think)))
	b.WriteString(line(t, assistant(sid, "feature/biz-277-a", "2026-11-01T10:00:06.000Z", "msg_1", "claude-opus-5-5", 10, 20, 100, 1000, tool)))
	b.WriteString(line(t, map[string]any{"type": "user", "sessionId": sid, "cwd": "/work/app", "gitBranch": "feature/biz-277-a",
		"timestamp": "2026-11-01T10:00:07.000Z", "message": map[string]any{"role": "user",
			"content": []map[string]any{{"type": "tool_result", "content": "SECRET-TOOL-OUTPUT"}}},
		"toolUseResult": map[string]any{"stdout": "SECRET-TOOL-OUTPUT"}}))
	b.WriteString(line(t, assistant(sid, "feature/biz-278-b", "2026-11-01T10:30:00.000Z", "msg_2", "claude-opus-5-5", 1, 2, 3, 4, text)))
	b.WriteString(line(t, assistant(sid, "feature/biz-278-b", "2026-11-01T10:40:00.000Z", "msg_3", "claude-sonnet-5-5", 5, 6, 7, 8, text)))
	b.WriteString(line(t, assistant(sid, "", "2026-11-01T10:41:00.000Z", "msg_s", "<synthetic>", 0, 0, 0, 0, text)))
	b.WriteString(line(t, assistant("other-session", "main", "2026-10-01T00:00:00.000Z", "msg_x", "claude-opus-5-5", 999, 999, 999, 999, text)))
	b.WriteString("this line is not json\n")
	b.WriteString(line(t, map[string]any{"type": "summary", "summary": "SECRET-PROMPT-TEXT"}))
	return b.String()
}

func subagentTranscript(t *testing.T) string {
	return line(t, assistant(sid, "feature/biz-278-b", "2026-11-01T10:50:00.000Z", "msg_4", "claude-haiku-5-5", 1, 1, 1, 1,
		[]map[string]any{{"type": "text", "text": "SECRET-ANSWER-TEXT"}}))
}

func TestParseTranscriptSumsPerModel(t *testing.T) {
	s, ok, err := parseTranscript(sid, strings.NewReader(mainTranscript(t)), strings.NewReader(subagentTranscript(t)))
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
	if s.Messages != 4 { // msg_1 (two lines), msg_2, msg_3, msg_4; synthetic and foreign lines skipped
		t.Fatalf("messages = %d", s.Messages)
	}
	got := map[string]models.SessionUsage{}
	for _, u := range s.Usage {
		got[u.Model] = u
	}
	want := map[string]models.SessionUsage{
		"claude-opus-5-5":   {Model: "claude-opus-5-5", InputTokens: 11, OutputTokens: 22, CacheReadTokens: 103, CacheWriteTokens: 1004},
		"claude-sonnet-5-5": {Model: "claude-sonnet-5-5", InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8},
		"claude-haiku-5-5":  {Model: "claude-haiku-5-5", InputTokens: 1, OutputTokens: 1, CacheReadTokens: 1, CacheWriteTokens: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("usage = %+v", s.Usage)
	}
	for m, w := range want {
		if got[m] != w {
			t.Errorf("%s = %+v, want %+v", m, got[m], w)
		}
	}
}

func TestParseTranscriptFallsBackToFirstSession(t *testing.T) {
	// A file whose name is not its session id: the first session in it wins.
	s, ok, err := parseTranscript("not-the-id", strings.NewReader(mainTranscript(t)))
	if err != nil || !ok || s.ExternalID != sid {
		t.Fatalf("got %q ok=%v err=%v", s.ExternalID, ok, err)
	}
	// Nothing an assistant said → nothing to send.
	_, ok, err = parseTranscript("x", strings.NewReader(`{"type":"user","sessionId":"x","timestamp":"2026-11-01T10:00:00Z"}`+"\n"))
	if err != nil || ok {
		t.Fatalf("empty session: ok=%v err=%v", ok, err)
	}
}

// fakeServer records each PUT and answers like DoneWhen would.
type fakeServer struct {
	mu     sync.Mutex
	bodies map[string]string
	auth   []string
	wsHdr  []string
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	f := &fakeServer{bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		if r.Method != http.MethodPut || id == r.URL.Path {
			http.Error(w, "bad route", 404)
			return
		}
		f.mu.Lock()
		f.bodies[id] = string(raw)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.wsHdr = append(f.wsHdr, r.Header.Get("X-Workspace"))
		f.mu.Unlock()
		var in models.AgentSession
		_ = json.Unmarshal(raw, &in)
		in.ExternalID = id
		for _, u := range in.Usage {
			in.Tokens += u.Tokens()
		}
		in.CostUSD = 1.5
		_ = json.NewEncoder(w).Encode(in)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func writeTranscript(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(p, []byte(mainTranscript(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, sid, "subagents")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "agent-a1.jsonl"), []byte(subagentTranscript(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

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
	body, ok := f.bodies[sid]
	if !ok || len(f.bodies) != 1 {
		t.Fatalf("bodies = %v", f.bodies)
	}
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
	if strings.Join(keys, ",") != "endedAt,gitBranch,issueKeys,messages,projectPath,startedAt,usage" {
		t.Fatalf("body fields = %v", keys)
	}
	if f.auth[0] != "Bearer dw_test" || f.wsHdr[0] != "biz" {
		t.Fatalf("headers: auth %q ws %q", f.auth[0], f.wsHdr[0])
	}
	// 11+22+103+1004 + 5+6+7+8 + 4 = 1170 tokens.
	if want := "sent " + sid + " 1170 $1.5000\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

func TestPushDirSince(t *testing.T) {
	f, srv := newFakeServer(t)
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "proj-a"))
	old := filepath.Join(dir, "proj-b", "22222222-0000-0000-0000-000000000000.jsonl")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	oldBody := strings.ReplaceAll(mainTranscript(t), sid, "22222222-0000-0000-0000-000000000000")
	if err := os.WriteFile(old, []byte(oldBody), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runSessions([]string{"push", "--dir", dir, "--since", "2026-06-01"},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "dw_test"}), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	// The subagent file is part of its session, never a session of its own.
	if len(f.bodies) != 1 || f.bodies[sid] == "" {
		t.Fatalf("sent %d sessions: %v", len(f.bodies), f.bodies)
	}
	// Without --since both go.
	f.bodies = map[string]string{}
	if code := runSessions([]string{"push", "--dir", dir},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "dw_test"}), &out, &errOut); code != 0 || len(f.bodies) != 2 {
		t.Fatalf("exit %d, sent %d", code, len(f.bodies))
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
	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if got, _ = os.ReadFile(marker); len(got) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.TrimSpace(string(got)) != "sessions push --file "+transcript {
		t.Fatalf("donewhen called with %q", got)
	}
	// Bad input, a missing binary: still exit 0.
	run("not json", "DONEWHEN_BIN="+stub)
	run("", "DONEWHEN_BIN="+stub)
	run(`{"transcript_path":"/x.jsonl"}`, "DONEWHEN_BIN="+filepath.Join(dir, "missing"))
}
