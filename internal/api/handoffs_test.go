package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnreginald/donewhen/internal/config"
	"github.com/johnreginald/donewhen/internal/models"
	"github.com/johnreginald/donewhen/internal/service"
	"github.com/johnreginald/donewhen/internal/store"
)

func TestHandoffVersionsAndValidation(t *testing.T) {
	e := newEnv(t, config.Config{})
	uid, _ := e.user("")
	a := e.workspace(uid)
	b := e.workspace(uid)
	tok := e.token(uid, a.ID)
	ini, err := e.store.SaveInitiative(context.Background(), a.ID, models.Initiative{Name: "POS"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/initiatives/" + ini.ID + "/handoff"
	tmpl := service.HandoffTemplate()

	put := func(body, summary string) (int, map[string]any) {
		w := e.do("PUT", base, map[string]string{"body": body, "summary": summary}, bearer(tok))
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	if c, m := put(tmpl+"first text", "one"); c != 200 || m["version"] != float64(1) {
		t.Fatalf("first save = %d %v", c, m)
	}
	if c, m := put(tmpl+"second text", "two"); c != 200 || m["version"] != float64(2) || m["actor"] != "ai" {
		t.Fatalf("second save = %d %v", c, m)
	}

	get := func(q string) map[string]any {
		w := e.do("GET", base+q, nil, bearer(tok))
		if w.Code != 200 {
			t.Fatalf("GET %s = %d %s", q, w.Code, w.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	if m := get(""); m["bodyMd"] != tmpl+"second text" || m["version"] != float64(2) {
		t.Fatalf("latest = %v", m)
	}
	if m := get("?version=1"); m["bodyMd"] != tmpl+"first text" {
		t.Fatalf("version 1 = %v", m)
	}
	if w := e.do("GET", base+"?version=9", nil, bearer(tok)); w.Code != 404 {
		t.Fatalf("missing version = %d", w.Code)
	}

	w := e.do("GET", base+"/versions", nil, bearer(tok))
	var list []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 || list[0]["version"] != float64(2) || list[0]["summary"] != "two" || list[0]["bodyMd"] != nil {
		t.Fatalf("versions = %s", w.Body.String())
	}

	for name, in := range map[string][2]string{
		"empty summary": {tmpl, "  "},
		"long summary":  {tmpl, strings.Repeat("x", 201)},
	} {
		w := e.do("PUT", base, map[string]string{"body": in[0], "summary": in[1]}, bearer(tok))
		var m map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		if w.Code != 400 || m["code"] != "invalid_handoff" {
			t.Fatalf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}

	// Another workspace's initiative is refused, for read and write.
	other := e.token(uid, b.ID)
	if w := e.do("GET", base, nil, bearer(other)); w.Code != 404 {
		t.Fatalf("cross-workspace GET = %d", w.Code)
	}
	if w := e.do("PUT", base, map[string]string{"body": tmpl, "summary": "y"}, bearer(other)); w.Code != 404 {
		t.Fatalf("cross-workspace PUT = %d", w.Code)
	}
	if w := e.do("GET", base+"/versions", nil, bearer(other)); w.Code != 404 {
		t.Fatalf("cross-workspace versions = %d", w.Code)
	}

	// Each save wrote one activity entry.
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity WHERE workspace_id=$1 AND kind='handoff_saved' AND detail LIKE 'Handoff updated: %'`, a.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("activity entries = %d (%v)", n, err)
	}
}

func TestHandoffStructure(t *testing.T) {
	e := newEnv(t, config.Config{})
	uid, _ := e.user("")
	a := e.workspace(uid)
	tok := e.token(uid, a.ID)
	ini, err := e.store.SaveInitiative(context.Background(), a.ID, models.Initiative{Name: "POS"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/initiatives/" + ini.ID + "/handoff"
	tmpl := service.HandoffTemplate()
	put := func(in map[string]any) (int, map[string]any) {
		w := e.do("PUT", base, in, bearer(tok))
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}

	// A missing heading is refused and listed.
	noWaiting := strings.Replace(tmpl, "## Waiting on owner\n\n", "", 1)
	c, m := put(map[string]any{"body": noWaiting, "summary": "s"})
	miss, _ := m["missing"].([]any)
	if c != 400 || m["code"] != "handoff_sections_missing" || len(miss) != 1 || miss[0] != "## Waiting on owner" {
		t.Fatalf("missing heading = %d %v", c, m)
	}
	// Heading match ignores case and surrounding space.
	loose := strings.ReplaceAll(tmpl, "## Now", "## now  ")
	if c, m := put(map[string]any{"body": loose, "summary": "s"}); c != 200 {
		t.Fatalf("loose heading = %d %v", c, m)
	}

	// Too long, by lines and by characters.
	c, m = put(map[string]any{"body": tmpl + strings.Repeat("line\n", 180), "summary": "s"})
	if c != 400 || m["code"] != "handoff_too_long" || m["lines"].(float64) < 180 {
		t.Fatalf("180 lines = %d %v", c, m)
	}
	c, m = put(map[string]any{"body": tmpl + strings.Repeat("x", 12001), "summary": "s"})
	if c != 400 || m["code"] != "handoff_too_long" {
		t.Fatalf("12001 chars = %d %v", c, m)
	}

	// In flight names a Done ticket, an open one and an unknown one: saved, with a warning for the Done one only.
	st := e.store
	done, err := st.CreateIssue(context.Background(), a.ID, store.IssueInput{Title: "d", StateName: "Canceled"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := st.CreateIssue(context.Background(), a.ID, store.IssueInput{Title: "o", StateName: "Backlog"})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(tmpl, "## In flight\n", "## In flight\n\n| "+done.Key+" | x |\n| "+open.Key+" | y |\n| ZZZ-999 | z |\n", 1)
	// A key outside In flight is not looked at.
	body = strings.Replace(body, "## Next steps\n", "## Next steps\n\n1. "+open.Key+"\n", 1)
	c, m = put(map[string]any{"body": body, "summary": "s"})
	dt, _ := m["done_tickets"].([]any)
	if c != 200 || len(dt) != 1 || dt[0] != done.Key {
		t.Fatalf("done tickets = %d %v", c, m)
	}

	// Legacy (import) skips the structure checks; old versions stay readable.
	if c, m := put(map[string]any{"body": "old format", "summary": "s", "legacy": true}); c != 200 || m["version"] != float64(2+1) {
		t.Fatalf("legacy = %d %v", c, m)
	}
	w := e.do("GET", base, nil, bearer(tok))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "old format") {
		t.Fatalf("read legacy = %d %s", w.Code, w.Body.String())
	}

	// The format endpoint serves the one constant.
	w = e.do("GET", "/api/handoff/format", nil, bearer(tok))
	var f service.HandoffFormat
	if err := json.Unmarshal(w.Body.Bytes(), &f); w.Code != 200 || err != nil || f.Template != tmpl || len(f.Sections) != 6 || f.MaxLines != 150 || f.MaxChars != 12000 {
		t.Fatalf("format = %d %s", w.Code, w.Body.String())
	}
}
