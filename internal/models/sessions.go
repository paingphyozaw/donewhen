package models

import (
	"regexp"
	"strings"
	"time"
)

// AgentSession is one Claude Code session as DoneWhen records it: where it
// ran, which tickets its branches named, and how many tokens it used. It holds
// numbers and metadata only, never message text.
type AgentSession struct {
	ID          string         `json:"id"`
	ExternalID  string         `json:"externalId"` // Claude's sessionId; unique per workspace
	ProjectPath string         `json:"projectPath"`
	GitBranch   string         `json:"gitBranch"`
	IssueKeys   []string       `json:"issueKeys"`
	StartedAt   time.Time      `json:"startedAt"`
	EndedAt     time.Time      `json:"endedAt"`
	Messages    int            `json:"messages"`
	Usage       []SessionUsage `json:"usage"`
	Tokens      int64          `json:"tokens"` // sum of all four counts over all models
	CostUSD     float64        `json:"costUsd"`
	Kinds       []KindCost     `json:"kinds"`  // cost split by token kind (BIZ-282)
	Agents      []SessionRun   `json:"agents"` // the session's subagent runs (BIZ-282)
}

// SessionAgent is one subagent run as the importer reads it from the run's
// agent-<id>.meta.json. Description is the short task title the coordinator
// gave the run; it is the only free text DoneWhen stores. IssueKey is set by
// the server: the ticket key the description names, if any.
type SessionAgent struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Model       string `json:"model"`
	IssueKey    string `json:"issueKey,omitempty"`
}

// SessionRun is a subagent run with the usage of its messages in the session.
type SessionRun struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Issue       string  `json:"issue"`
	Model       string  `json:"model"`
	Messages    int     `json:"messages"`
	Tokens      int64   `json:"tokens"`
	CostUSD     float64 `json:"costUsd"`
}

// Token kinds of the cost split, in their fixed order.
const (
	KindInput      = "input"
	KindOutput     = "output"
	KindCacheRead  = "cache_read"
	KindCacheWrite = "cache_write"
)

// TokenKinds is the fixed order of the kinds.
var TokenKinds = []string{KindInput, KindOutput, KindCacheRead, KindCacheWrite}

// KindCost is the tokens and cost of one token kind.
type KindCost struct {
	Kind    string  `json:"kind"`
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
}

// SessionUsage is one model's token sums within a session.
// CacheWriteTokens is the sum of the 5-minute and 1-hour cache writes.
type SessionUsage struct {
	Model              string  `json:"model"`
	InputTokens        int64   `json:"inputTokens"`
	OutputTokens       int64   `json:"outputTokens"`
	CacheReadTokens    int64   `json:"cacheReadTokens"`
	CacheWriteTokens   int64   `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64   `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64   `json:"cacheWrite1hTokens"`
	CostUSD            float64 `json:"costUsd"`
}

// SessionMessage is the usage of one model response, with the branch it was
// made on. Message ids are unique
// per workspace: the first session that sends an id owns it, so a resumed
// session that repeats earlier lines never counts them twice.
type SessionMessage struct {
	ID                 string    `json:"id"`
	Model              string    `json:"model"`
	At                 time.Time `json:"at"`
	Branch             string    `json:"branch"`             // gitBranch of the message's line
	IssueKey           string    `json:"issueKey,omitempty"` // set by the server from Branch
	InputTokens        int64     `json:"inputTokens"`
	OutputTokens       int64     `json:"outputTokens"`
	CacheReadTokens    int64     `json:"cacheReadTokens"`
	CacheWrite5mTokens int64     `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64     `json:"cacheWrite1hTokens"`
	CostUSD            float64   `json:"costUsd"`
	AgentID            string    `json:"agentId,omitempty"` // the subagent run that made it; "" is the coordinator
}

// Tokens is the sum of the four counts.
func (u SessionUsage) Tokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// SessionSummaryRow is one group of GET /api/sessions/summary. Messages and
// CacheReadTokens are set for every group; the other fields only for the
// groups named on them.
type SessionSummaryRow struct {
	Key             string  `json:"key"`
	Sessions        int     `json:"sessions"`
	Messages        int     `json:"messages"`
	Tokens          int64   `json:"tokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	CostUSD         float64 `json:"costUsd"`

	Runs int `json:"runs,omitempty"` // agent_type: subagent runs (coordinator: sessions)

	// agent: one row per run; Key is the agent id.
	Type        string     `json:"type,omitempty"`
	Description string     `json:"description,omitempty"`
	Issue       string     `json:"issue,omitempty"`
	Model       string     `json:"model,omitempty"`
	Session     string     `json:"session,omitempty"` // external id of the run's session
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`

	// ticket: Coordinator marks the hub coordinator's overhead on its branch
	// key; CostByType and RunsByType split the row by agent type.
	Coordinator bool               `json:"coordinator,omitempty"`
	CostByType  map[string]float64 `json:"costByType,omitempty"`
	RunsByType  map[string]int     `json:"runsByType,omitempty"`
	LastAt      *time.Time         `json:"lastAt,omitempty"`
}

// branchKeyRe finds a ticket key at the start of a branch path segment:
// `feature/biz-277-home` → biz-277. The prefix is 2-6 letters or digits
// starting with a letter, so `release-2026` is not read as a ticket.
var branchKeyRe = regexp.MustCompile(`(?:^|/)([A-Za-z][A-Za-z0-9]{1,5})-([0-9]+)(?:$|[-/_.])`)

// textKeyRe finds an upper-case ticket key anywhere in a run's description:
// "Build BIZ-278 sessions backend" → BIZ-278.
var textKeyRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([A-Z][A-Z0-9]{1,5}-[0-9]+)(?:$|[^A-Za-z0-9])`)

// IssueKeyFromText returns the first upper-case ticket key a short text
// names, or "".
func IssueKeyFromText(s string) string {
	if m := textKeyRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// IssueKeysFromBranch returns the ticket keys a branch name carries, upper
// case, in order, without repeats. nil when there is none.
func IssueKeysFromBranch(branch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range branchKeyRe.FindAllStringSubmatch(branch, -1) {
		k := strings.ToUpper(m[1]) + "-" + m[2]
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
