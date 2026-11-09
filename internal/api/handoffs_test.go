package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnreginald/donewhen/internal/config"
	"github.com/johnreginald/donewhen/internal/models"
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

	put := func(body, summary string) (int, map[string]any) {
		w := e.do("PUT", base, map[string]string{"body": body, "summary": summary}, bearer(tok))
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	if c, m := put("first text", "one"); c != 200 || m["version"] != float64(1) {
		t.Fatalf("first save = %d %v", c, m)
	}
	if c, m := put("second text", "two"); c != 200 || m["version"] != float64(2) || m["actor"] != "ai" {
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
	if m := get(""); m["bodyMd"] != "second text" || m["version"] != float64(2) {
		t.Fatalf("latest = %v", m)
	}
	if m := get("?version=1"); m["bodyMd"] != "first text" {
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
		"empty summary": {"text", "  "},
		"empty body":    {"", "s"},
		"long summary":  {"text", strings.Repeat("x", 201)},
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
	if w := e.do("PUT", base, map[string]string{"body": "x", "summary": "y"}, bearer(other)); w.Code != 404 {
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
