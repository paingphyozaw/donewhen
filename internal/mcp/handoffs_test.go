package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnreginald/donewhen/internal/models"
)

func TestHandoffTools(t *testing.T) {
	e := newEnv(t)
	uid := e.user()
	a := e.workspace(uid)
	b := e.workspace(uid)
	ctx := e.ctxFor(uid, a.ID)
	if _, err := e.store.SaveInitiative(context.Background(), a.ID, models.Initiative{Name: "POS"}); err != nil {
		t.Fatal(err)
	}
	bIni, err := e.store.SaveInitiative(context.Background(), b.ID, models.Initiative{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}

	out, isErr := e.call(ctx, "get_handoff", map[string]any{"initiative": "pos"})
	if isErr || !strings.Contains(out, "No handoff yet. Use save_handoff.") || !strings.Contains(out, `"handoff":null`) {
		t.Fatalf("empty get_handoff = %q (err %v)", out, isErr)
	}

	out, isErr = e.call(ctx, "save_handoff", map[string]any{"initiative": "POS", "body": "# Full text\n\nline", "summary": "first"})
	if isErr || !strings.Contains(out, `"version":1`) {
		t.Fatalf("save_handoff = %q (err %v)", out, isErr)
	}
	e.call(ctx, "save_handoff", map[string]any{"initiative": "POS", "body": "second", "summary": "second"})

	out, isErr = e.call(ctx, "get_handoff", map[string]any{"initiative": "POS"})
	var h models.Handoff
	if err := json.Unmarshal([]byte(out), &h); isErr || err != nil || h.BodyMD != "second" || h.Version != 2 || h.Actor != "ai" {
		t.Fatalf("get_handoff = %q (err %v %v)", out, isErr, err)
	}
	out, _ = e.call(ctx, "get_handoff", map[string]any{"initiative": "POS", "version": 1})
	if err := json.Unmarshal([]byte(out), &h); err != nil || h.BodyMD != "# Full text\n\nline" {
		t.Fatalf("version 1 = %q", out)
	}

	if out, isErr = e.call(ctx, "save_handoff", map[string]any{"initiative": "POS", "body": "x", "summary": ""}); !isErr || !strings.Contains(out, "invalid_handoff") {
		t.Fatalf("empty summary = %q (err %v)", out, isErr)
	}
	// A token pinned to workspace a cannot reach b's initiative.
	if out, isErr = e.call(ctx, "save_handoff", map[string]any{"initiative": bIni.ID, "body": "x", "summary": "y"}); !isErr {
		t.Fatalf("cross-workspace save = %q", out)
	}
}

// A name in another workspace is not found by a pinned token; with all
// workspaces in reach, a name shared by two workspaces is refused.
func TestHandoffToolsNameScope(t *testing.T) {
	e := newEnv(t)
	uid := e.user()
	a := e.workspace(uid)
	b := e.workspace(uid)
	only := "Only-" + uniq()
	shared := "Shared-" + uniq()
	for _, w := range []models.Workspace{a, b} {
		if _, err := e.store.SaveInitiative(context.Background(), w.ID, models.Initiative{Name: shared}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.SaveInitiative(context.Background(), b.ID, models.Initiative{Name: only}); err != nil {
		t.Fatal(err)
	}

	pinned := e.ctxFor(uid, a.ID)
	for _, tool := range []string{"get_handoff", "save_handoff"} {
		out, isErr := e.call(pinned, tool, map[string]any{"initiative": only, "body": "x", "summary": "y"})
		if !isErr || !strings.Contains(out, "not found") {
			t.Fatalf("%s by name in another workspace = %q (err %v)", tool, out, isErr)
		}
	}

	all := e.ctxFor(uid, "")
	for _, tool := range []string{"get_handoff", "save_handoff"} {
		out, isErr := e.call(all, tool, map[string]any{"initiative": shared, "body": "x", "summary": "y"})
		if !isErr || !strings.Contains(out, "matches 2 projects") {
			t.Fatalf("%s with a shared name = %q (err %v)", tool, out, isErr)
		}
	}
	// Naming the workspace resolves it.
	if out, isErr := e.call(all, "save_handoff", map[string]any{"initiative": shared, "body": "x", "summary": "y", "workspace": a.ID}); isErr {
		t.Fatalf("save with workspace = %q", out)
	}
	if out, isErr := e.call(all, "get_handoff", map[string]any{"initiative": shared, "version": 5, "workspace": a.ID}); !isErr || !strings.Contains(out, "version 5 not found") {
		t.Fatalf("missing version = %q (err %v)", out, isErr)
	}
}
