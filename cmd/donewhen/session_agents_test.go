package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnreginald/donewhen/internal/store"
)

// BIZ-282: the importer reads each subagent run's agent-<id>.meta.json and
// sends who made each message. Fixtures are written by hand.

// writeAgentMeta adds the meta files Claude Code writes next to the two
// subagent transcripts of writeTranscript. Fields the importer must not send
// carry SECRET-* values.
func writeAgentMeta(t *testing.T, dir string) {
	t.Helper()
	write(t, filepath.Join(dir, sid, "subagents", "agent-a1.meta.json"),
		`{"agentType":"implementer","launchedWithDefaults":true,"description":"Build BIZ-9 x","model":"claude-haiku-5-5",`+
			`"toolUseId":"SECRET-TOOL-USE-ID","spawnDepth":1,"requestShape":"SECRET-SHAPE","finished":true}`)
	write(t, filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "agent-b2.meta.json"),
		`{"agentType":"reviewer","description":"Review BIZ-9","parentAgentId":"SECRET-PARENT","spawnDepth":2,"finished":true}`)
}

func TestPushSendsAgents(t *testing.T) {
	f, srv := newFakeServer(t)
	dir := t.TempDir()
	path := writeTranscript(t, dir)
	writeAgentMeta(t, dir)
	env := envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "t"})
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"push", "--file", path}, env, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	body := f.bodies[sid][0]
	for _, s := range []string{"SECRET-TOOL-USE-ID", "SECRET-SHAPE", "SECRET-PARENT", "toolUseId", "spawnDepth"} {
		if strings.Contains(body, s) {
			t.Fatalf("body leaks %q: %s", s, body)
		}
	}
	var p sessionPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	want := []agentPayload{
		{ID: "a1", Type: "implementer", Description: "Build BIZ-9 x", Model: "claude-haiku-5-5"},
		{ID: "b2", Type: "reviewer", Description: "Review BIZ-9"},
	}
	if len(p.Agents) != len(want) || p.Agents[0] != want[0] || p.Agents[1] != want[1] {
		t.Fatalf("agents = %+v", p.Agents)
	}
	by := map[string]string{}
	for _, m := range p.Messages {
		by[m.ID] = m.AgentID
	}
	if by["msg_1"] != "" || by["msg_2"] != "" || by["msg_4"] != "a1" || by["msg_5"] != "b2" {
		t.Fatalf("agent per message = %v", by)
	}

	// A subagent file alone carries its own run.
	sub := filepath.Join(dir, sid, "subagents", "workflows", "wf_1", "agent-b2.jsonl")
	if code := runSessions([]string{"push", "--file", sub}, env, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var alone sessionPayload
	_ = json.Unmarshal([]byte(f.bodies[sid][1]), &alone)
	if len(alone.Agents) != 1 || alone.Agents[0] != want[1] || len(alone.Messages) != 1 || alone.Messages[0].AgentID != "b2" {
		t.Fatalf("subagent alone = %+v", alone)
	}
}

// A run without a meta file is still a run: its id is sent, with no type.
func TestPushAgentWithoutMeta(t *testing.T) {
	f, srv := newFakeServer(t)
	path := writeTranscript(t, t.TempDir())
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"push", "--file", path},
		envOf(map[string]string{"DONEWHEN_URL": srv.URL, "DONEWHEN_TOKEN": "t"}), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var p sessionPayload
	_ = json.Unmarshal([]byte(f.bodies[sid][0]), &p)
	if len(p.Agents) != 2 || p.Agents[0].ID != "a1" || p.Agents[0].Type != "" {
		t.Fatalf("agents = %+v", p.Agents)
	}
}

// End to end: the runs' messages count for the ticket their description names.
func TestPushAgentsAgainstServer(t *testing.T) {
	url, tok, st, wsID := realServer(t)
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "proj"))
	writeAgentMeta(t, filepath.Join(dir, "proj"))
	var out, errOut bytes.Buffer
	if code := runSessions([]string{"push", "--dir", dir}, envOf(map[string]string{"DONEWHEN_URL": url, "DONEWHEN_TOKEN": tok}),
		&out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	rows, err := st.SessionSummary(context.Background(), wsID, store.SessionFilter{}, store.SessionGroupIssue)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Key] = r.Tokens
	}
	// msg_4 (4 tokens) and msg_5 (8) are BIZ-9's; msg_1 is BIZ-277's; msg_2, msg_3 BIZ-278's.
	if got["BIZ-9"] != 12 || got["BIZ-277"] != 1130 || got["BIZ-278"] != 36 {
		t.Fatalf("issue tokens = %v", got)
	}
	types, err := st.SessionSummary(context.Background(), wsID, store.SessionFilter{}, store.SessionGroupAgentType)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 {
		t.Fatalf("agent types = %+v", types)
	}
}
