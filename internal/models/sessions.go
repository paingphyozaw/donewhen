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
}

// Tokens is the sum of the four counts.
func (u SessionUsage) Tokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// SessionSummaryRow is one group of GET /api/sessions/summary.
type SessionSummaryRow struct {
	Key      string  `json:"key"`
	Sessions int     `json:"sessions"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"costUsd"`
}

// branchKeyRe finds a ticket key at the start of a branch path segment:
// `feature/biz-277-home` → biz-277. The prefix is 2-6 letters or digits
// starting with a letter, so `release-2026` is not read as a ticket.
var branchKeyRe = regexp.MustCompile(`(?:^|/)([A-Za-z][A-Za-z0-9]{1,5})-([0-9]+)(?:$|[-/_.])`)

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
