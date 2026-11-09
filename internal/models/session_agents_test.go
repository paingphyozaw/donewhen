package models

import (
	"math"
	"testing"
)

func TestIssueKeyFromText(t *testing.T) {
	prefixes := []string{"BIZ", "CLT"} // the workspace's own prefix first
	for in, want := range map[string]string{
		"Build BIZ-278 sessions backend": "BIZ-278",
		"Review BIZ-9":                   "BIZ-9",
		"Final check BIZ-121 11c3a9f":    "BIZ-121",
		"Fix BIZ-119..125 order":         "BIZ-119",
		"(CLT-4) hotfix":                 "CLT-4",
		"Fix UTF-8 bug for BIZ-12":       "BIZ-12",
		"Parse ISO-8601 dates":           "",
		"Port CLT-3 then BIZ-7":          "BIZ-7", // the workspace's own prefix wins
		"Find the sessions code":         "",
		"biz-12 lower case":              "",
		"XBIZ-12x":                       "",
		"":                               "",
	} {
		if got := IssueKeyFromText(in, prefixes); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if got := IssueKeyFromText("Build BIZ-1", nil); got != "" {
		t.Errorf("no known prefix → %q", got)
	}
}

func TestKindCostsAddUpToStored(t *testing.T) {
	// opus 5.5: in 1M $4, out 0.1M $2, read 2M $0.40, 5m write 0.4M $2, 1h write 0.5M $4 = $12.40
	k := KindCosts("claude-opus-5-5", 1_000_000, 100_000, 2_000_000, 400_000, 500_000, 12.4)
	want := [4]float64{4, 2, 0.4, 6}
	for i := range k {
		if math.Abs(k[i]-want[i]) > 1e-9 {
			t.Fatalf("kinds = %v, want %v", k, want)
		}
	}
	// Stored under an older, higher price: scaled to what was stored.
	k = KindCosts("claude-opus-5-5", 1_000_000, 100_000, 0, 0, 0, 12)
	if math.Abs(k[0]-8) > 1e-9 || math.Abs(k[1]-4) > 1e-9 {
		t.Fatalf("scaled kinds = %v", k)
	}
	if k := KindCosts("no-such-model", 1, 1, 1, 1, 1, 1); k != [4]float64{} {
		t.Fatalf("unknown model = %v", k)
	}
}
