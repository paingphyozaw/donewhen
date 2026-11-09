package service

import (
	"context"

	"github.com/johnreginald/donewhen/internal/models"
)

// HandoffSaved is a saved version plus the tickets in its In flight section
// that are already Done or Canceled (a warning, never a refusal).
type HandoffSaved struct {
	models.Handoff
	DoneTickets []string `json:"done_tickets"`
}

// SaveHandoff checks the structure of the body, appends a handoff version and
// writes one activity entry. legacy skips the structure checks: only the
// import path sets it, for old files. The entry is best-effort, like the
// others the service writes.
func (s *Service) SaveHandoff(ctx context.Context, wsID, initiativeID, body, summary, actor string, legacy bool) (HandoffSaved, error) {
	if !legacy {
		if err := CheckHandoffFormat(body); err != nil {
			return HandoffSaved{}, err
		}
	}
	h, err := s.Store.SaveHandoff(ctx, wsID, initiativeID, body, summary, actor)
	if err != nil {
		return HandoffSaved{Handoff: h}, err
	}
	title := ""
	if ini, err := s.Store.GetInitiative(ctx, wsID, initiativeID); err == nil {
		title = ini.Name
	}
	s.logActivity(ctx, wsID, models.Activity{
		IssueTitle: title, Actor: h.Actor, Kind: "handoff_saved", Detail: "Handoff updated: " + h.Summary,
	})
	return HandoffSaved{Handoff: h, DoneTickets: s.HandoffDoneTickets(ctx, wsID, body)}, nil
}

// HandoffDoneTickets returns the tickets named in the In flight section that
// are Done or Canceled, looked up in the same workspace. Unknown keys are
// ignored. Never nil, so it serialises as [].
func (s *Service) HandoffDoneTickets(ctx context.Context, wsID, body string) []string {
	keys := HandoffInFlightKeys(body)
	if len(keys) > handoffMaxLookup {
		keys = keys[:handoffMaxLookup]
	}
	out := []string{}
	if len(keys) == 0 {
		return out
	}
	cats, err := s.Store.IssueCategoriesByKeys(ctx, wsID, keys)
	if err != nil {
		return out
	}
	for _, k := range keys { // keep the order they appear in
		if c := cats[k]; c == "completed" || c == "canceled" {
			out = append(out, k)
		}
	}
	return out
}

// handoffMaxLookup caps how many distinct keys one save looks up.
const handoffMaxLookup = 100
