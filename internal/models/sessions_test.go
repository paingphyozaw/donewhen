package models

import (
	"math"
	"reflect"
	"testing"
)

func TestIssueKeysFromBranch(t *testing.T) {
	cases := map[string][]string{
		"feature/biz-277-home":        {"BIZ-277"},
		"feature/BIZ-12":              {"BIZ-12"},
		"biz-5-x":                     {"BIZ-5"},
		"feature/clt-3-fix/biz-9-sub": {"CLT-3", "BIZ-9"},
		"main":                        nil,
		"HEAD":                        nil,
		"":                            nil,
		"release-2026":                nil, // a release branch is not a ticket
		"feature/biz278":              nil,
		"feature/v2-x":                nil,
	}
	for branch, want := range cases {
		if got := IssueKeysFromBranch(branch); !reflect.DeepEqual(got, want) {
			t.Errorf("IssueKeysFromBranch(%q) = %v, want %v", branch, got, want)
		}
	}
}

func TestPriceFor(t *testing.T) {
	p, ok := PriceFor("claude-opus-5-5")
	if !ok || p.Input != 4 || p.Output != 20 {
		t.Fatalf("opus 5.5 = %+v %v", p, ok)
	}
	// The longest prefix wins: fable-5-1 is not priced as fable-5.
	f51, _ := PriceFor("claude-fable-5-1")
	f5, _ := PriceFor("claude-fable-5")
	if f51.CacheRead == f5.CacheRead {
		t.Fatalf("fable 5.1 cache read %v should differ from fable 5 %v", f51.CacheRead, f5.CacheRead)
	}
	// A dated or suffixed id still matches its family.
	if _, ok := PriceFor("claude-sonnet-4-5-20250929"); !ok {
		t.Fatal("dated sonnet id not priced")
	}
	for _, m := range []string{"", "<synthetic>", "gpt-5", "claude-unknown-9"} {
		if _, ok := PriceFor(m); ok {
			t.Errorf("PriceFor(%q) should be unknown", m)
		}
	}
}

func TestSessionUsageCost(t *testing.T) {
	u := SessionUsage{Model: "claude-opus-5-5", InputTokens: 1_000_000, OutputTokens: 1_000_000,
		CacheReadTokens: 1_000_000, CacheWriteTokens: 1_000_000}
	p, _ := PriceFor(u.Model)
	want := p.Input + p.Output + p.CacheRead + p.CacheWrite
	if got := UsageCost(u); math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
	if got := UsageCost(SessionUsage{Model: "mystery", InputTokens: 5_000_000}); got != 0 {
		t.Fatalf("unknown model cost = %v, want 0", got)
	}
	if got := u.Tokens(); got != 4_000_000 {
		t.Fatalf("tokens = %d", got)
	}
}
