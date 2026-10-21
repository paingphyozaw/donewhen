package store

import (
	"reflect"
	"testing"
)

func keys(items []NextItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}

func TestRankNextPutsTheCriticalPathFirst(t *testing.T) {
	// A → B → C → D is the long chain; E unlocks F only; G stands alone but is urgent.
	nodes := []NextNode{
		{ID: "a", Key: "K-1", Ready: true, Number: 1},
		{ID: "b", Key: "K-2", Ready: true, Number: 2},
		{ID: "c", Key: "K-3", Number: 3},
		{ID: "d", Key: "K-4", Number: 4},
		{ID: "e", Key: "K-5", Ready: true, Number: 5},
		{ID: "f", Key: "K-6", Number: 6},
		{ID: "g", Key: "K-7", Ready: true, Priority: 1, Number: 7},
	}
	edges := []NextEdge{{"b", "a"}, {"c", "b"}, {"d", "c"}, {"f", "e"}}
	got := RankNext(nodes, edges, 0)
	if want := []string{"K-1", "K-5", "K-7"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("order %v, want %v (K-2 waits on K-1, so it is not ready)", keys(got), want)
	}
	if got[0].Chain != 4 || got[0].Unlocks != 3 || !reflect.DeepEqual(got[0].ChainKeys, []string{"K-1", "K-2", "K-3", "K-4"}) {
		t.Fatalf("head %+v", got[0])
	}
}

func TestRankNextTiesGoToUnlocksThenPriorityThenAge(t *testing.T) {
	// a and b both head a chain of 2; a unlocks two tickets, b one.
	nodes := []NextNode{
		{ID: "a", Key: "K-1", Ready: true, Number: 1, Priority: 4},
		{ID: "b", Key: "K-2", Ready: true, Number: 2, Priority: 1},
		{ID: "x", Key: "K-3", Number: 3}, {ID: "y", Key: "K-4", Number: 4}, {ID: "z", Key: "K-5", Number: 5},
		{ID: "p", Key: "K-6", Ready: true, Number: 6},              // no priority
		{ID: "q", Key: "K-7", Ready: true, Number: 7, Priority: 3}, // medium
		{ID: "r", Key: "K-8", Ready: true, Number: 8, Priority: 3}, // medium, newer
	}
	edges := []NextEdge{{"x", "a"}, {"y", "a"}, {"z", "b"}}
	got := keys(RankNext(nodes, edges, 0))
	if want := []string{"K-1", "K-2", "K-7", "K-8", "K-6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestRankNextIgnoresDoneBlockersLimitsAndSurvivesACycle(t *testing.T) {
	nodes := []NextNode{
		{ID: "a", Key: "K-1", Ready: true, Number: 1},
		{ID: "b", Key: "K-2", Number: 2},
		{ID: "c", Key: "K-3", Number: 3},
		{ID: "d", Key: "K-4", Ready: true, Number: 4},
	}
	// "gone" is a done blocker: not in nodes, so it does not hold K-4 back.
	edges := []NextEdge{{"b", "a"}, {"c", "b"}, {"b", "c"}, {"d", "gone"}}
	got := RankNext(nodes, edges, 1)
	if len(got) != 1 || got[0].Key != "K-1" {
		t.Fatalf("got %v, want only K-1", keys(got))
	}
}
