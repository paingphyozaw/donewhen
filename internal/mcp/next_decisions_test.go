package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnreginald/donewhen/internal/models"
	"github.com/johnreginald/donewhen/internal/store"
)

// A decision is a comment of its own kind; get_issue hands it back so the next
// agent reads it before working on the ticket.
func TestDecisionsComeBackWithGetIssue(t *testing.T) {
	e := newEnv(t)
	uid := e.user()
	w := e.workspace(uid)
	ctx := e.ctxFor(uid, w.ID)

	out, isErr := e.call(ctx, "save_issue", map[string]any{"title": "purchase", "state": "Ready"})
	if isErr {
		t.Fatal(out)
	}
	var is models.Issue
	_ = json.Unmarshal([]byte(out), &is)

	if out, isErr = e.call(ctx, "save_comment", map[string]any{"issueId": is.Key, "body": "just a note"}); isErr {
		t.Fatal(out)
	}
	out, isErr = e.call(ctx, "save_comment", map[string]any{"issueId": is.Key, "body": "Store keepers see cost", "kind": "decision"})
	if isErr {
		t.Fatal(out)
	}
	var c models.Comment
	_ = json.Unmarshal([]byte(out), &c)
	if c.Kind != "decision" {
		t.Fatalf("kind %q", c.Kind)
	}
	if out, isErr = e.call(ctx, "save_comment", map[string]any{"issueId": is.Key, "body": "x", "kind": "blocked_reason"}); !isErr {
		t.Fatalf("a caller wrote a blocked_reason: %s", out)
	}

	out, isErr = e.call(ctx, "get_issue", map[string]any{"id": is.Key})
	if isErr {
		t.Fatal(out)
	}
	var got struct {
		Key       string           `json:"key"`
		Decisions []models.Comment `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Key != is.Key || len(got.Decisions) != 1 || got.Decisions[0].BodyMD != "Store keepers see cost" {
		t.Fatalf("get_issue: %s", out)
	}
}

// next_issues: a Ready ticket that waits on an open one is not offered; the head
// of the longest chain comes first.
func TestNextIssuesFollowsTheCriticalPath(t *testing.T) {
	e := newEnv(t)
	uid := e.user()
	w := e.workspace(uid)
	ctx := e.ctxFor(uid, w.ID)

	mk := func(title, state string, blockedBy ...string) string {
		args := map[string]any{"title": title, "state": state}
		if len(blockedBy) > 0 {
			args["blockedBy"] = blockedBy
		}
		out, isErr := e.call(ctx, "save_issue", args)
		if isErr {
			t.Fatal(out)
		}
		var is models.Issue
		_ = json.Unmarshal([]byte(out), &is)
		return is.Key
	}
	lone := mk("lone", "Ready")
	head := mk("head", "Ready")
	mid := mk("mid", "Ready", head)
	mk("tail", "Backlog", mid)
	mk("dropped", "Canceled")
	free := mk("free", "Ready", mk("finished", "Canceled"))

	out, isErr := e.call(ctx, "next_issues", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	var items []store.NextItem
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, it := range items {
		order = append(order, it.Key)
	}
	if strings.Join(order, ",") != strings.Join([]string{head, lone, free}, ",") {
		t.Fatalf("order %v, want %v %v %v (mid waits on head)", order, head, lone, free)
	}
	if items[0].Chain != 3 || items[0].Unlocks != 2 {
		t.Fatalf("head %+v", items[0])
	}
}
