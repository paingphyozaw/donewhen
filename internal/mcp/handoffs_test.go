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
