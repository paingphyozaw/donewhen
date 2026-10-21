package store

import (
	"context"
	"sort"
	"strings"
)

// NextItem is one ticket that can start now, with how much work it opens up.
type NextItem struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Priority int    `json:"priority"`
	// Unlocks: open tickets that wait on this one, directly or further down.
	Unlocks int `json:"unlocks"`
	// Chain: the longest run of open tickets that starts with this one —
	// its length in tickets (1 = nothing waits on it) and their keys.
	Chain     int      `json:"chain"`
	ChainKeys []string `json:"chainKeys"`
}

// NextNode and NextEdge are the open work the ranking reads.
type NextNode struct {
	ID, Key, Title, State string
	Ready                 bool // in the state work starts from
	Priority, Number      int
}

// NextEdge: Issue waits on Blocker; both are open.
type NextEdge struct{ Issue, Blocker string }

// NextIssues ranks the tickets that can start now: in Ready (or, when the
// workspace has no state called Ready, any not-started state), with no open
// blocker. The ticket at the head of the longest chain of open work comes
// first — the critical path — then the one that unlocks most, then priority
// (urgent first, none last), then the oldest. Issues of archived epics are
// left out.
func (s *Store) NextIssues(ctx context.Context, wsID string, limit int) ([]NextItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.key, i.title, st.name, st.category, i.priority, i.number
		FROM issues i JOIN workflow_states st ON st.id = i.state_id
		WHERE i.workspace_id = $1
		  AND st.category NOT IN ('completed', 'canceled')
		  AND (i.project_id IS NULL OR i.project_id NOT IN (SELECT id FROM projects WHERE status = 'archived'))`, wsID)
	if err != nil {
		return nil, err
	}
	type raw struct {
		n        NextNode
		category string
	}
	var all []raw
	hasReady := false
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.n.ID, &r.n.Key, &r.n.Title, &r.n.State, &r.category, &r.n.Priority, &r.n.Number); err != nil {
			rows.Close()
			return nil, err
		}
		if strings.EqualFold(r.n.State, "Ready") {
			hasReady = true
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	nodes := make([]NextNode, len(all))
	for i, r := range all {
		r.n.Ready = strings.EqualFold(r.n.State, "Ready") || (!hasReady && r.category == "unstarted")
		nodes[i] = r.n
	}
	links, err := s.ListBlockLinks(ctx, wsID)
	if err != nil {
		return nil, err
	}
	edges := make([]NextEdge, 0, len(links))
	for _, l := range links {
		if !l.Done {
			edges = append(edges, NextEdge{Issue: l.IssueID, Blocker: l.BlockerID})
		}
	}
	return RankNext(nodes, edges, limit), nil
}

// RankNext is the ranking of NextIssues on data already loaded. Edges whose
// ends are not both in nodes are ignored (a done or archived blocker is not
// in the way).
func RankNext(nodes []NextNode, edges []NextEdge, limit int) []NextItem {
	byID := make(map[string]NextNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	waitsOn := map[string]int{}       // open blockers per issue
	unblocks := map[string][]string{} // blocker → issues waiting on it
	for _, e := range edges {
		if _, ok := byID[e.Issue]; !ok {
			continue
		}
		if _, ok := byID[e.Blocker]; !ok {
			continue
		}
		waitsOn[e.Issue]++
		unblocks[e.Blocker] = append(unblocks[e.Blocker], e.Issue)
	}
	for k := range unblocks {
		sort.Slice(unblocks[k], func(i, j int) bool { return byID[unblocks[k][i]].Number < byID[unblocks[k][j]].Number })
	}

	// Longest chain from each ticket. The store refuses cycles, but an old
	// row could still hold one: a ticket already on the current path counts
	// as a dead end, so this always ends.
	chain := map[string][]string{}
	onPath := map[string]bool{}
	var longest func(id string) []string
	longest = func(id string) []string {
		if c, ok := chain[id]; ok {
			return c
		}
		if onPath[id] {
			return nil
		}
		onPath[id] = true
		var best []string
		for _, next := range unblocks[id] {
			if c := longest(next); len(c) > len(best) {
				best = c
			}
		}
		onPath[id] = false
		c := append([]string{id}, best...)
		chain[id] = c
		return c
	}
	reach := func(id string) int {
		seen := map[string]bool{id: true}
		stack := []string{id}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, next := range unblocks[cur] {
				if !seen[next] {
					seen[next] = true
					stack = append(stack, next)
				}
			}
		}
		return len(seen) - 1
	}

	out := []NextItem{}
	for _, n := range nodes {
		if !n.Ready || waitsOn[n.ID] > 0 {
			continue
		}
		c := longest(n.ID)
		keys := make([]string, len(c))
		for i, id := range c {
			keys[i] = byID[id].Key
		}
		out = append(out, NextItem{
			ID: n.ID, Key: n.Key, Title: n.Title, State: n.State, Priority: n.Priority,
			Unlocks: reach(n.ID), Chain: len(c), ChainKeys: keys,
		})
	}
	prio := func(p int) int { // urgent (1) first, none (0) last
		if p == 0 {
			return 5
		}
		return p
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Chain != b.Chain {
			return a.Chain > b.Chain
		}
		if a.Unlocks != b.Unlocks {
			return a.Unlocks > b.Unlocks
		}
		if prio(a.Priority) != prio(b.Priority) {
			return prio(a.Priority) < prio(b.Priority)
		}
		return byID[a.ID].Number < byID[b.ID].Number
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
