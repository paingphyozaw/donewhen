package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/johnreginald/donewhen/internal/config"
	"github.com/johnreginald/donewhen/internal/models"
)

// BIZ-278: sessions and token usage.

func sessionBody(branch, started, ended string, usage ...map[string]any) map[string]any {
	return map[string]any{
		"projectPath": "/home/dev/app",
		"gitBranch":   branch,
		"startedAt":   started,
		"endedAt":     ended,
		"messages":    3,
		"usage":       usage,
	}
}

func usageRow(model string, in, out, cr, cw int64) map[string]any {
	return map[string]any{"model": model, "inputTokens": in, "outputTokens": out,
		"cacheReadTokens": cr, "cacheWriteTokens": cw}
}

func decodeSession(t *testing.T, w *httptest.ResponseRecorder) models.AgentSession {
	t.Helper()
	var s models.AgentSession
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode session: %v (%s)", err, w.Body.String())
	}
	return s
}

func listSessions(t *testing.T, e *testEnv, query string, opts ...reqOpt) []models.AgentSession {
	t.Helper()
	r := e.do("GET", "/api/sessions"+query, nil, opts...)
	if r.Code != 200 {
		t.Fatalf("list %s: %d %s", query, r.Code, r.Body.String())
	}
	var out []models.AgentSession
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func summary(t *testing.T, e *testEnv, query string, opts ...reqOpt) map[string]models.SessionSummaryRow {
	t.Helper()
	r := e.do("GET", "/api/sessions/summary"+query, nil, opts...)
	if r.Code != 200 {
		t.Fatalf("summary %s: %d %s", query, r.Code, r.Body.String())
	}
	var rows []models.SessionSummaryRow
	if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	out := map[string]models.SessionSummaryRow{}
	for _, row := range rows {
		out[row.Key] = row
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestSessionsPutIsIdempotent(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))

	body := sessionBody("feature/biz-277-home", "2026-11-01T10:00:00Z", "2026-11-01T11:00:00Z",
		usageRow("claude-opus-5-5", 1_000_000, 100_000, 2_000_000, 500_000),
		usageRow("claude-haiku-5-5", 10_000, 1_000, 0, 0))
	r := e.do("PUT", "/api/sessions/sess-1", body, ai)
	if r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	s := decodeSession(t, r)
	// opus 5.5: 1M*4 + 0.1M*20 + 2M*0.20 + 0.5M*8 = 4 + 2 + 0.4 + 4 = 10.4
	// haiku 5.5: 0.01M*0.10 + 0.001M*0.50 = 0.001 + 0.0005 = 0.0015
	if s.ExternalID != "sess-1" || len(s.Usage) != 2 || !near(s.CostUSD, 10.4015) {
		t.Fatalf("saved = %+v", s)
	}
	if s.Tokens != 3_611_000 {
		t.Fatalf("tokens = %d", s.Tokens)
	}
	if len(s.IssueKeys) != 1 || s.IssueKeys[0] != "BIZ-277" {
		t.Fatalf("issue keys = %v", s.IssueKeys)
	}

	list := listSessions(t, e, "", ai)
	if len(list) != 1 || !near(list[0].CostUSD, 10.4015) || list[0].Messages != 3 {
		t.Fatalf("list = %+v", list)
	}

	// The same session again with new numbers replaces, never doubles.
	body = sessionBody("feature/biz-277-home", "2026-11-01T10:00:00Z", "2026-11-01T12:00:00Z",
		usageRow("claude-opus-5-5", 2_000_000, 0, 0, 0))
	if r := e.do("PUT", "/api/sessions/sess-1", body, ai); r.Code != 200 {
		t.Fatalf("put again: %d %s", r.Code, r.Body.String())
	}
	list = listSessions(t, e, "", ai)
	if len(list) != 1 || len(list[0].Usage) != 1 || list[0].Tokens != 2_000_000 || !near(list[0].CostUSD, 8) {
		t.Fatalf("after replace = %+v", list)
	}
	var rows int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM ai_session_usage u JOIN ai_sessions s ON s.id=u.session_id
		WHERE s.workspace_id=$1`, w.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("usage rows = %d (%v)", rows, err)
	}

	// An unknown model is saved at cost 0.
	body = sessionBody("main", "2026-11-02T10:00:00Z", "2026-11-02T10:05:00Z",
		usageRow("some-other-model", 1000, 1000, 0, 0))
	r = e.do("PUT", "/api/sessions/sess-2", body, ai)
	if r.Code != 200 {
		t.Fatalf("put unknown model: %d %s", r.Code, r.Body.String())
	}
	if s := decodeSession(t, r); s.CostUSD != 0 || s.Tokens != 2000 {
		t.Fatalf("unknown model = %+v", s)
	}

	// A browser session can save too (cookie + CSRF).
	if r := e.do("PUT", "/api/sessions/sess-3", body, browser(e.session(owner))); r.Code != 200 {
		t.Fatalf("browser put: %d %s", r.Code, r.Body.String())
	}
	// Without credentials it cannot.
	if r := e.do("PUT", "/api/sessions/sess-4", body); r.Code != 401 {
		t.Fatalf("anonymous put: %d", r.Code)
	}
}

func TestSessionsInvalidBody(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))
	good := func() map[string]any {
		return sessionBody("main", "2026-11-01T10:00:00Z", "2026-11-01T11:00:00Z",
			usageRow("claude-opus-5-5", 1, 1, 1, 1))
	}
	cases := map[string]func(map[string]any){
		"message text field": func(b map[string]any) { b["text"] = "hello" },
		"no start":           func(b map[string]any) { delete(b, "startedAt") },
		"end before start":   func(b map[string]any) { b["endedAt"] = "2026-11-01T09:00:00Z" },
		"negative tokens":    func(b map[string]any) { b["usage"] = []map[string]any{usageRow("m", -1, 0, 0, 0)} },
		"empty model":        func(b map[string]any) { b["usage"] = []map[string]any{usageRow("", 1, 0, 0, 0)} },
		"repeated model": func(b map[string]any) {
			b["usage"] = []map[string]any{usageRow("m", 1, 0, 0, 0), usageRow("m", 2, 0, 0, 0)}
		},
		"bad issue key":     func(b map[string]any) { b["issueKeys"] = []string{"not a key"} },
		"negative messages": func(b map[string]any) { b["messages"] = -1 },
	}
	for name, mutate := range cases {
		b := good()
		mutate(b)
		r := e.do("PUT", "/api/sessions/bad", b, ai)
		var resp map[string]string
		_ = json.Unmarshal(r.Body.Bytes(), &resp)
		if r.Code != 400 || resp["code"] != "invalid_session" {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	if got := listSessions(t, e, "", ai); len(got) != 0 {
		t.Fatalf("an invalid session was saved: %+v", got)
	}
}

func TestSessionsListAndSummary(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))

	put := func(id string, body map[string]any) {
		t.Helper()
		if r := e.do("PUT", "/api/sessions/"+id, body, ai); r.Code != 200 {
			t.Fatalf("put %s: %d %s", id, r.Code, r.Body.String())
		}
	}
	// Day 1: two sessions; day 2: one. Opus 5.5 at 1M input = $4.
	a := sessionBody("feature/biz-277-x", "2026-11-01T09:00:00Z", "2026-11-01T10:00:00Z",
		usageRow("claude-opus-5-5", 1_000_000, 0, 0, 0))
	b := sessionBody("feature/biz-278-y", "2026-11-01T15:00:00Z", "2026-11-01T16:00:00Z",
		usageRow("claude-opus-5-5", 1_000_000, 0, 0, 0), usageRow("claude-sonnet-5-5", 1_000_000, 0, 0, 0))
	b["issueKeys"] = []string{"BIZ-277"} // a branch seen earlier in the session
	b["projectPath"] = "/home/dev/other"
	c := sessionBody("main", "2026-11-02T09:00:00Z", "2026-11-02T09:30:00Z",
		usageRow("claude-sonnet-5-5", 500_000, 0, 0, 0))
	put("a", a)
	put("b", b)
	put("c", c)

	list := listSessions(t, e, "", ai)
	if len(list) != 3 || list[0].ExternalID != "c" || list[2].ExternalID != "a" {
		t.Fatalf("not newest first: %+v", list)
	}
	if got := listSessions(t, e, "?limit=1", ai); len(got) != 1 || got[0].ExternalID != "c" {
		t.Fatalf("limit: %+v", got)
	}
	if got := listSessions(t, e, "?issue=BIZ-277", ai); len(got) != 2 {
		t.Fatalf("issue filter: %+v", got)
	}
	if got := listSessions(t, e, "?project=/home/dev/other", ai); len(got) != 1 || got[0].ExternalID != "b" {
		t.Fatalf("project filter: %+v", got)
	}
	if got := listSessions(t, e, "?from=2026-11-02", ai); len(got) != 1 || got[0].ExternalID != "c" {
		t.Fatalf("from filter: %+v", got)
	}
	if got := listSessions(t, e, "?to=2026-11-01", ai); len(got) != 2 {
		t.Fatalf("to filter (whole day): %+v", got)
	}

	day := summary(t, e, "?group=day", ai)
	if len(day) != 2 {
		t.Fatalf("day rows = %+v", day)
	}
	// Day 1: a=$4, b=$4+$2 → $10, 3M tokens. Day 2: c=$1, 0.5M tokens.
	if r := day["2026-11-01"]; r.Sessions != 2 || r.Tokens != 3_000_000 || !near(r.CostUSD, 10) {
		t.Fatalf("day 1 = %+v", r)
	}
	if r := day["2026-11-02"]; r.Sessions != 1 || r.Tokens != 500_000 || !near(r.CostUSD, 1) {
		t.Fatalf("day 2 = %+v", r)
	}

	issue := summary(t, e, "?group=issue", ai)
	if r := issue["BIZ-277"]; r.Sessions != 2 || !near(r.CostUSD, 10) {
		t.Fatalf("BIZ-277 = %+v", r)
	}
	if r := issue["BIZ-278"]; r.Sessions != 1 || !near(r.CostUSD, 6) {
		t.Fatalf("BIZ-278 = %+v", r)
	}
	if r := issue[""]; r.Sessions != 1 { // c names no ticket
		t.Fatalf("no ticket = %+v", r)
	}

	model := summary(t, e, "?group=model", ai)
	if r := model["claude-opus-5-5"]; r.Sessions != 2 || r.Tokens != 2_000_000 || !near(r.CostUSD, 8) {
		t.Fatalf("opus = %+v", r)
	}
	if r := model["claude-sonnet-5-5"]; r.Sessions != 2 || r.Tokens != 1_500_000 || !near(r.CostUSD, 3) {
		t.Fatalf("sonnet = %+v", r)
	}

	project := summary(t, e, "?group=project&from=2026-11-01T00:00:00Z&to=2026-11-01T23:59:59Z", ai)
	if len(project) != 2 || project["/home/dev/app"].Sessions != 1 || !near(project["/home/dev/other"].CostUSD, 6) {
		t.Fatalf("project = %+v", project)
	}

	for _, q := range []string{"?group=week", "?group=day&from=yesterday", "?limit=x"} {
		path := "/api/sessions/summary" + q
		if q == "?limit=x" {
			path = "/api/sessions" + q
		}
		if r := e.do("GET", path, nil, ai); r.Code != 400 {
			t.Errorf("%s: %d", path, r.Code)
		}
	}
}

func TestSessionsWorkspaceGuard(t *testing.T) {
	e := newEnv(t, config.Config{})
	alice, _ := e.user("")
	wa := e.workspace(alice)
	bob, _ := e.user("")
	wb := e.workspace(bob)

	body := sessionBody("main", "2026-11-01T10:00:00Z", "2026-11-01T11:00:00Z",
		usageRow("claude-opus-5-5", 1, 1, 1, 1))
	if r := e.do("PUT", "/api/sessions/secret", body, bearer(e.token(alice, wa.ID))); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}

	bobTok := e.token(bob, "")
	// Bob naming Alice's workspace is refused on every route.
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/sessions"},
		{"GET", "/api/sessions/summary?group=day"},
		{"PUT", "/api/sessions/secret"},
	} {
		var b any
		if req.method == "PUT" {
			b = body
		}
		if r := e.do(req.method, req.path, b, bearer(bobTok), header("X-Workspace", wa.ID)); r.Code != 403 {
			t.Errorf("%s %s as bob in alice's workspace: %d", req.method, req.path, r.Code)
		}
	}
	// A token pinned to Bob's workspace cannot be pointed at Alice's.
	if r := e.do("GET", "/api/sessions", nil, bearer(e.token(bob, wb.ID)), header("X-Workspace", wa.ID)); r.Code != 403 {
		t.Errorf("pinned token in another workspace: %d", r.Code)
	}
	// In his own workspace Bob sees none of Alice's sessions.
	if got := listSessions(t, e, "", bearer(bobTok), header("X-Workspace", wb.ID)); len(got) != 0 {
		t.Fatalf("bob sees %+v", got)
	}
	if got := summary(t, e, "?group=model", bearer(bobTok), header("X-Workspace", wb.ID)); len(got) != 0 {
		t.Fatalf("bob summary %+v", got)
	}
	// The same external id in Bob's workspace is a separate session.
	if r := e.do("PUT", "/api/sessions/secret", body, bearer(bobTok), header("X-Workspace", wb.ID)); r.Code != 200 {
		t.Fatalf("bob put: %d %s", r.Code, r.Body.String())
	}
	if got := listSessions(t, e, "", bearer(e.token(alice, wa.ID))); len(got) != 1 {
		t.Fatalf("alice now has %d sessions", len(got))
	}
}
