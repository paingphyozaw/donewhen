package service

import (
	"context"

	"github.com/johnreginald/donewhen/internal/models"
)

// SaveHandoff appends a handoff version and writes one activity entry. The
// entry is best-effort, like the others the service writes.
func (s *Service) SaveHandoff(ctx context.Context, wsID, initiativeID, body, summary, actor string) (models.Handoff, error) {
	h, err := s.Store.SaveHandoff(ctx, wsID, initiativeID, body, summary, actor)
	if err != nil {
		return h, err
	}
	title := ""
	if ini, err := s.Store.GetInitiative(ctx, wsID, initiativeID); err == nil {
		title = ini.Name
	}
	s.logActivity(ctx, wsID, models.Activity{
		IssueTitle: title, Actor: h.Actor, Kind: "handoff_saved", Detail: "Handoff updated: " + h.Summary,
	})
	return h, nil
}
