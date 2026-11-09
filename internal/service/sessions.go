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
	maxSessionUsageRows  = 50
	maxSessionIssueKeys  = 100
)

var issueKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

func invalidSession(format string, args ...any) error {
	return fmt.Errorf("%w: %s", store.ErrInvalid, fmt.Sprintf(format, args...))
}

// SaveSession checks a session, adds the ticket keys its branch names, prices
// each usage row with the current price table and saves it, replacing an
// earlier save of the same session. It publishes session.saved and writes no
// activity entry: sessions are noise in the Log.
func (s *Service) SaveSession(ctx context.Context, wsID string, in models.AgentSession, actor string) (models.AgentSession, error) {
	if err := cleanSession(&in); err != nil {
		return models.AgentSession{}, err
	}
	for i := range in.Usage {
		in.Usage[i].CostUSD = models.UsageCost(in.Usage[i])
	}
	saved, err := s.Store.UpsertSession(ctx, wsID, in)
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

// cleanSession validates a session in place. Every error wraps store.ErrInvalid.
func cleanSession(in *models.AgentSession) error {
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
	if in.Messages < 0 {
		return invalidSession("messages must not be negative")
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

	if len(in.Usage) > maxSessionUsageRows {
		return invalidSession("too many usage rows")
	}
	seenModel := map[string]bool{}
	for _, u := range in.Usage {
		if n := utf8.RuneCountInString(u.Model); n < 1 || n > maxSessionModel {
			return invalidSession("model must be 1-%d characters", maxSessionModel)
		}
		if seenModel[u.Model] {
			return invalidSession("model %q is listed twice", u.Model)
		}
		seenModel[u.Model] = true
		if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 {
			return invalidSession("token counts must not be negative")
		}
	}
	return nil
}
