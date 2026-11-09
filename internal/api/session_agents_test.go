package api

import (
	"encoding/json"
	"testing"

	"github.com/johnreginald/donewhen/internal/config"
	"github.com/johnreginald/donewhen/internal/models"
)

// BIZ-282: who made each message (coordinator or which subagent run), the
// cost split by token kind, and the analysis groups.

func byAgent(m map[string]any, agentID string) map[string]any {
	m["agentId"] = agentID
	return m
}

func agentReq(id, typ, desc string) map[string]any {
	return map[string]any{"id": id, "type": typ, "description": desc}
}

func summaryRows(t *testing.T, e *testEnv, query string, opts ...reqOpt) []models.SessionSummaryRow {
	t.Helper()
	r := e.do("GET", "/api/sessions/summary"+query, nil, opts...)
	if r.Code != 200 {
		t.Fatalf("summary %s: %d %s", query, r.Code, r.Body.String())
	}
	var rows []models.SessionSummaryRow
	if err := json.Unmarshal(r.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// hubSession is a coordinator on the hub branch feature/biz-102-app-list that
// runs an implementer and a reviewer for BIZ-9 and an Explore run with no key.
// Opus 5.5: $4 per 1M input tokens.
func hubSession(withAgents bool) map[string]any {
	const br = "feature/biz-102-app-list"
	m := func(id string, in int64, agent string) map[string]any {
		x := onBranch(msg(id, "claude-opus-5-5", "2026-11-05T10:00:00Z", in, 0, 0, 0, 0), br)
		if withAgents && agent != "" {
			byAgent(x, agent)
		}
		return x
	}
	body := sessionBody(br, "2026-11-05T09:00:00Z", "2026-11-05T11:00:00Z",
		m("c1", 1_000_000, ""),         // coordinator $4
		m("i1", 500_000, "ag-impl"),    // implementer $2
		m("i2", 500_000, "ag-impl"),    // implementer $2
		m("r1", 250_000, "ag-rev"),     // reviewer $1
		m("x1", 250_000, "ag-explore")) // Explore, no key in its description $1
	if withAgents {
		body["agents"] = []map[string]any{
			agentReq("ag-impl", "implementer", "Build BIZ-9 x"),
			agentReq("ag-rev", "reviewer", "Review BIZ-9"),
			agentReq("ag-explore", "Explore", "Find the sessions code"),
		}
	}
	return body
}

func TestSessionAgentsAttribution(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))

	if r := e.do("PUT", "/api/sessions/hub", hubSession(true), ai); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	issue := summary(t, e, "?group=issue", ai)
	// Subagents whose description names BIZ-9 count for BIZ-9; the coordinator
	// and the Explore run without a key keep the branch key.
	if r := issue["BIZ-9"]; !near(r.CostUSD, 5) || r.Messages != 3 {
		t.Fatalf("BIZ-9 = %+v", r)
	}
	if r := issue["BIZ-102"]; !near(r.CostUSD, 5) || r.Messages != 2 {
		t.Fatalf("BIZ-102 = %+v", r)
	}

	types := summary(t, e, "?group=agent_type", ai)
	if r := types["coordinator"]; r.Runs != 1 || !near(r.CostUSD, 4) {
		t.Fatalf("coordinator = %+v", r)
	}
	if r := types["implementer"]; r.Runs != 1 || !near(r.CostUSD, 4) || r.Messages != 2 {
		t.Fatalf("implementer = %+v", r)
	}
	if r := types["reviewer"]; r.Runs != 1 || !near(r.CostUSD, 1) {
		t.Fatalf("reviewer = %+v", r)
	}
	if len(types) != 4 {
		t.Fatalf("types = %+v", types)
	}

	agents := summary(t, e, "?group=agent", ai)
	if len(agents) != 3 {
		t.Fatalf("agent rows = %+v", agents)
	}
	if r := agents["ag-impl"]; r.Type != "implementer" || r.Description != "Build BIZ-9 x" || r.Issue != "BIZ-9" ||
		r.Model != "claude-opus-5-5" || r.Messages != 2 || !near(r.CostUSD, 4) || r.StartedAt == nil || r.Session != "hub" {
		t.Fatalf("ag-impl = %+v", r)
	}
	if r := agents["ag-explore"]; r.Issue != "BIZ-102" {
		t.Fatalf("a run without a key takes its branch key: %+v", r)
	}
	if got := summary(t, e, "?group=agent&issue=BIZ-9", ai); len(got) != 2 {
		t.Fatalf("agent rows for BIZ-9 = %+v", got)
	}

	// The ticket rows keep the hub coordinator apart: the coordinator and the
	// keyless run count as coordinator overhead on BIZ-102, not as BIZ-102 work.
	var b9, hub *models.SessionSummaryRow
	rows := summaryRows(t, e, "?group=ticket", ai)
	for i := range rows {
		switch {
		case rows[i].Key == "BIZ-9" && !rows[i].Coordinator:
			b9 = &rows[i]
		case rows[i].Key == "BIZ-102" && rows[i].Coordinator:
			hub = &rows[i]
		case rows[i].Key == "BIZ-102":
			t.Fatalf("BIZ-102 holds hub work as ticket work: %+v", rows[i])
		}
	}
	if b9 == nil || hub == nil || len(rows) != 2 {
		t.Fatalf("ticket rows = %+v", rows)
	}
	if !near(b9.CostByType["implementer"], 4) || !near(b9.CostByType["reviewer"], 1) ||
		b9.RunsByType["implementer"] != 1 || b9.RunsByType["reviewer"] != 1 || b9.LastAt == nil {
		t.Fatalf("BIZ-9 ticket row = %+v", b9)
	}
	if !near(hub.CostUSD, 5) || !near(hub.CostByType["coordinator"], 4) {
		t.Fatalf("hub row = %+v", hub)
	}

	// The session list carries its runs and its cost by kind.
	list := listSessions(t, e, "", ai)
	if len(list) != 1 || len(list[0].Agents) != 3 || len(list[0].Kinds) != 4 {
		t.Fatalf("list = %+v", list)
	}
	for _, a := range list[0].Agents {
		if a.ID == "ag-rev" && (a.Type != "reviewer" || !near(a.CostUSD, 1)) {
			t.Fatalf("listed run = %+v", a)
		}
	}
}

// A ticket session (one opened on its own branch, no runs for other tickets)
// is ticket work, not coordinator overhead.
func TestSessionTicketRowsWithoutHub(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))
	body := sessionBody("feature/biz-50-x", "2026-11-05T09:00:00Z", "2026-11-05T11:00:00Z",
		onBranch(msg("t1", "claude-opus-5-5", "2026-11-05T10:00:00Z", 1_000_000, 0, 0, 0, 0), "feature/biz-50-x"),
		byAgent(onBranch(msg("t2", "claude-opus-5-5", "2026-11-05T10:01:00Z", 1_000_000, 0, 0, 0, 0), "feature/biz-50-x"), "ag-e"))
	body["agents"] = []map[string]any{agentReq("ag-e", "Explore", "Find files")}
	if r := e.do("PUT", "/api/sessions/s50", body, ai); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	rows := summaryRows(t, e, "?group=ticket", ai)
	if len(rows) != 1 || rows[0].Key != "BIZ-50" || rows[0].Coordinator || !near(rows[0].CostUSD, 8) {
		t.Fatalf("ticket rows = %+v", rows)
	}
}

func TestSessionAgentsRepushRefreshes(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))

	// First push from an older importer: no agents.
	if r := e.do("PUT", "/api/sessions/hub", hubSession(false), ai); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	before := listSessions(t, e, "", ai)[0]
	if r := summary(t, e, "?group=issue", ai)["BIZ-102"]; !near(r.CostUSD, 10) {
		t.Fatalf("before = %+v", r)
	}
	// Another session that repeats a message with an agent id: it does not own
	// the message, so nothing of it changes.
	other := sessionBody("feature/biz-77-z", "2026-11-05T09:00:00Z", "2026-11-05T11:00:00Z",
		byAgent(onBranch(msg("i1", "claude-opus-5-5", "2026-11-05T10:00:00Z", 500_000, 0, 0, 0, 0), "feature/biz-77-z"), "ag-foreign"))
	other["agents"] = []map[string]any{agentReq("ag-foreign", "implementer", "Build BIZ-77")}
	if r := e.do("PUT", "/api/sessions/other", other, ai); r.Code != 200 {
		t.Fatalf("put other: %d %s", r.Code, r.Body.String())
	}
	if _, ok := summary(t, e, "?group=issue", ai)["BIZ-77"]; ok {
		t.Fatal("a foreign session re-keyed a message it does not own")
	}

	// Re-push with agents: keys and agent ids are filled in.
	if r := e.do("PUT", "/api/sessions/hub", hubSession(true), ai); r.Code != 200 {
		t.Fatalf("re-put: %d %s", r.Code, r.Body.String())
	}
	issue := summary(t, e, "?group=issue", ai)
	if !near(issue["BIZ-9"].CostUSD, 5) || !near(issue["BIZ-102"].CostUSD, 5) {
		t.Fatalf("after re-push = %+v", issue)
	}
	after := listSessions(t, e, "?project=/home/dev/app", ai)
	var hub models.AgentSession
	for _, s := range after {
		if s.ExternalID == "hub" {
			hub = s
		}
	}
	if hub.Tokens != before.Tokens || !near(hub.CostUSD, before.CostUSD) || hub.Messages != before.Messages {
		t.Fatalf("tokens or cost changed: %+v → %+v", before, hub)
	}

	// A later push without agent ids (older importer again) keeps them.
	if r := e.do("PUT", "/api/sessions/hub", hubSession(false), ai); r.Code != 200 {
		t.Fatalf("old re-put: %d %s", r.Code, r.Body.String())
	}
	if r := summary(t, e, "?group=issue", ai)["BIZ-9"]; !near(r.CostUSD, 5) {
		t.Fatalf("an old push wiped the attribution: %+v", r)
	}
}

func TestSessionKindSplit(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))
	// opus 5.5: in 1M $4, out 0.1M $2, read 2M $0.40, 1h write 0.5M $4 → $10.40
	// haiku 5.5: in 10,000 $0.001, out 1,000 $0.0005
	body := sessionBody("main", "2026-11-01T10:00:00Z", "2026-11-01T11:00:00Z",
		msg("k1", "claude-opus-5-5", "2026-11-01T10:05:00Z", 1_000_000, 100_000, 2_000_000, 0, 500_000),
		msg("k2", "claude-haiku-5-5", "2026-11-01T10:10:00Z", 10_000, 1_000, 0, 0, 0),
		msg("k3", "unknown-model", "2026-11-01T10:11:00Z", 5, 5, 5, 5, 5))
	if r := e.do("PUT", "/api/sessions/k", body, ai); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	kind := summary(t, e, "?group=kind", ai)
	if len(kind) != 4 {
		t.Fatalf("kind rows = %+v", kind)
	}
	if r := kind["input"]; !near(r.CostUSD, 4.001) || r.Tokens != 1_010_005 {
		t.Fatalf("input = %+v", r)
	}
	if r := kind["output"]; !near(r.CostUSD, 2.0005) {
		t.Fatalf("output = %+v", r)
	}
	if r := kind["cache_read"]; !near(r.CostUSD, 0.4) || r.Tokens != 2_000_005 {
		t.Fatalf("cache_read = %+v", r)
	}
	if r := kind["cache_write"]; !near(r.CostUSD, 4) || r.Tokens != 500_010 {
		t.Fatalf("cache_write = %+v", r)
	}
	var total float64
	for _, r := range kind {
		total += r.CostUSD
	}
	s := listSessions(t, e, "", ai)[0]
	if !near(total, s.CostUSD) {
		t.Fatalf("kinds add up to %v, session costs %v", total, s.CostUSD)
	}
	// Every group carries messages and cache-read tokens.
	day := summary(t, e, "?group=day", ai)["2026-11-01"]
	if day.Messages != 3 || day.CacheReadTokens != 2_000_005 || len(day.Kinds) != 4 {
		t.Fatalf("day row = %+v", day)
	}
	// The day's kinds split its cost, in the fixed order.
	var dayTotal float64
	for i, k := range day.Kinds {
		if k.Kind != models.TokenKinds[i] {
			t.Fatalf("kind order = %+v", day.Kinds)
		}
		dayTotal += k.CostUSD
	}
	if !near(dayTotal, day.CostUSD) || !near(day.Kinds[2].CostUSD, 0.4) {
		t.Fatalf("day kinds = %+v (cost %v)", day.Kinds, day.CostUSD)
	}
	if r := summary(t, e, "?group=kind&from=2030-01-01", ai); len(r) != 0 {
		t.Fatalf("unexpected rows: %+v", r)
	}
}

func TestSessionAgentsInvalid(t *testing.T) {
	e := newEnv(t, config.Config{})
	owner, _ := e.user("")
	w := e.workspace(owner)
	ai := bearer(e.token(owner, w.ID))
	long := make([]byte, 301)
	for i := range long {
		long[i] = 'a'
	}
	for name, agents := range map[string][]map[string]any{
		"empty id":         {agentReq("", "implementer", "x")},
		"long description": {agentReq("a1", "implementer", string(long))},
		"listed twice":     {agentReq("a1", "implementer", "x"), agentReq("a1", "reviewer", "y")},
	} {
		body := sessionBody("main", "2026-11-01T10:00:00Z", "2026-11-01T11:00:00Z")
		body["agents"] = agents
		if r := e.do("PUT", "/api/sessions/bad", body, ai); r.Code != 400 {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
}
