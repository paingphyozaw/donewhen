package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/johnreginald/donewhen/internal/events"
	"github.com/johnreginald/donewhen/internal/models"
	"github.com/johnreginald/donewhen/internal/store"
)

const (
	maxSessionExternalID = 200
	maxSessionPath       = 1000
	maxSessionBranch     = 300
	maxSessionModel      = 100
	maxSessionMessages   = 5000 // per push; the importer sends 2,000
	maxSessionMessageID  = 200
	maxSessionIssueKeys  = 100
	maxSessionAgents     = 2000
	maxAgentType         = 100
	maxAgentDescription  = 300
)

var issueKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

func invalidSession(format string, args ...any) error {
	return fmt.Errorf("%w: %s", store.ErrInvalid, fmt.Sprintf(format, args...))
}

// SaveSession checks one push of a session, adds the ticket keys its branch
// names, prices each message with the current price table and saves it. A
// push is additive: messages already saved (by this or any other session of
// the workspace) are skipped. It publishes session.saved and writes no
// activity entry: sessions are noise in the Log.
//
// agents are the session's subagent runs (BIZ-282). A message's ticket key
// is the key its run's description names; else the key of its branch. The
// store applies the run's key, so a run saved by an earlier push still counts.
func (s *Service) SaveSession(ctx context.Context, wsID string, in models.AgentSession, msgs []models.SessionMessage, agents []models.SessionAgent, actor string) (models.AgentSession, error) {
	if err := cleanSession(&in, msgs); err != nil {
		return models.AgentSession{}, err
	}
	if err := cleanAgents(agents); err != nil {
		return models.AgentSession{}, err
	}
	for i := range agents {
		agents[i].IssueKey = models.IssueKeyFromText(agents[i].Description)
	}
	for i := range msgs {
		msgs[i].CostUSD = models.MessageCost(msgs[i])
		msgs[i].IssueKey = ""
		if keys := models.IssueKeysFromBranch(msgs[i].Branch); len(keys) > 0 {
			msgs[i].IssueKey = keys[0]
		}
	}
	saved, err := s.Store.SaveSession(ctx, wsID, in, msgs, agents)
	if err != nil {
		return saved, err
	}
	s.Bus.Publish(events.Event{
		Type:        events.SessionSaved,
		WorkspaceID: wsID,
		Actor:       actor,
		Session:     &saved,
	})
	return saved, nil
}

// cleanAgents validates the runs of a push in place. Every error wraps store.ErrInvalid.
func cleanAgents(agents []models.SessionAgent) error {
	if len(agents) > maxSessionAgents {
		return invalidSession("at most %d agents per request", maxSessionAgents)
	}
	seen := map[string]bool{}
	for i := range agents {
		a := &agents[i]
		a.ID = strings.TrimSpace(a.ID)
		if n := utf8.RuneCountInString(a.ID); n < 1 || n > maxSessionMessageID {
			return invalidSession("agent id must be 1-%d characters", maxSessionMessageID)
		}
		if seen[a.ID] {
			return invalidSession("agent %q is listed twice", a.ID)
		}
		seen[a.ID] = true
		a.Type = strings.TrimSpace(a.Type)
		a.Description = strings.TrimSpace(a.Description)
		if utf8.RuneCountInString(a.Type) > maxAgentType {
			return invalidSession("agent type is too long")
		}
		if utf8.RuneCountInString(a.Description) > maxAgentDescription {
			return invalidSession("agent description must be at most %d characters", maxAgentDescription)
		}
		if utf8.RuneCountInString(a.Model) > maxSessionModel {
			return invalidSession("agent model is too long")
		}
	}
	return nil
}

// cleanSession validates a session in place. Every error wraps store.ErrInvalid.
func cleanSession(in *models.AgentSession, msgs []models.SessionMessage) error {
	in.ExternalID = strings.TrimSpace(in.ExternalID)
	if n := utf8.RuneCountInString(in.ExternalID); n < 1 || n > maxSessionExternalID {
		return invalidSession("session id must be 1-%d characters", maxSessionExternalID)
	}
	if utf8.RuneCountInString(in.ProjectPath) > maxSessionPath {
		return invalidSession("projectPath is too long")
	}
	if utf8.RuneCountInString(in.GitBranch) > maxSessionBranch {
		return invalidSession("gitBranch is too long")
	}
	if in.StartedAt.IsZero() || in.EndedAt.IsZero() {
		return invalidSession("startedAt and endedAt are required")
	}
	if in.EndedAt.Before(in.StartedAt) {
		return invalidSession("endedAt is before startedAt")
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, k := range append(append([]string{}, in.IssueKeys...), models.IssueKeysFromBranch(in.GitBranch)...) {
		k = strings.ToUpper(strings.TrimSpace(k))
		if !issueKeyRe.MatchString(k) {
			return invalidSession("issue key %q is not a ticket key", k)
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) > maxSessionIssueKeys {
		return invalidSession("too many issue keys")
	}
	in.IssueKeys = keys

	if len(msgs) > maxSessionMessages {
		return invalidSession("at most %d messages per request", maxSessionMessages)
	}
	seenID := map[string]bool{}
	for _, m := range msgs {
		if n := utf8.RuneCountInString(m.ID); n < 1 || n > maxSessionMessageID {
			return invalidSession("message id must be 1-%d characters", maxSessionMessageID)
		}
		if seenID[m.ID] {
			return invalidSession("message %q is listed twice", m.ID)
		}
		seenID[m.ID] = true
		if utf8.RuneCountInString(m.AgentID) > maxSessionMessageID {
			return invalidSession("message agentId is too long")
		}
		if utf8.RuneCountInString(m.Branch) > maxSessionBranch {
			return invalidSession("message branch is too long")
		}
		if n := utf8.RuneCountInString(m.Model); n < 1 || n > maxSessionModel {
			return invalidSession("model must be 1-%d characters", maxSessionModel)
		}
		if m.InputTokens < 0 || m.OutputTokens < 0 || m.CacheReadTokens < 0 ||
			m.CacheWrite5mTokens < 0 || m.CacheWrite1hTokens < 0 {
			return invalidSession("token counts must not be negative")
		}
	}
	return nil
}
