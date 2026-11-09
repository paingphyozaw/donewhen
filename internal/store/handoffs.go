package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/johnreginald/donewhen/internal/models"
)

// ErrInvalidHandoff marks a save refused for its body or summary. It always
// travels with ErrInvalid, and the API answers it with code invalid_handoff.
var ErrInvalidHandoff = errors.New("invalid_handoff")

const (
	maxHandoffBody    = 200000
	maxHandoffSummary = 200
)

func invalidHandoff(format string, args ...any) error {
	return fmt.Errorf("%w: %w: "+format, append([]any{ErrInvalid, ErrInvalidHandoff}, args...)...)
}

const handoffCols = `version, initiative_id, summary, actor, created_at`

func scanHandoff(row pgx.Row, withBody bool) (models.Handoff, error) {
	var h models.Handoff
	if withBody {
		err := row.Scan(&h.Version, &h.InitiativeID, &h.Summary, &h.Actor, &h.CreatedAt, &h.BodyMD)
		return h, err
	}
	err := row.Scan(&h.Version, &h.InitiativeID, &h.Summary, &h.Actor, &h.CreatedAt)
	return h, err
}

// GetHandoff returns one version of an initiative's handoff: the latest when
// version is 0. ErrNotFound when the initiative is not in the workspace, when
// it has no handoff yet, or when that version does not exist.
func (s *Store) GetHandoff(ctx context.Context, wsID, initiativeID string, version int) (models.Handoff, error) {
	q := `SELECT ` + handoffCols + `, body_md FROM handoffs
	      WHERE initiative_id=$1 AND workspace_id=$2`
	args := []any{initiativeID, wsID}
	if version > 0 {
		q += ` AND version=$3`
		args = append(args, version)
	} else {
		q += ` ORDER BY version DESC LIMIT 1`
	}
	h, err := scanHandoff(s.pool.QueryRow(ctx, q, args...), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, ErrNotFound
	}
	return h, err
}

// ListHandoffVersions lists an initiative's versions, newest first, without
// their bodies.
func (s *Store) ListHandoffVersions(ctx context.Context, wsID, initiativeID string) ([]models.Handoff, error) {
	if _, err := s.GetInitiative(ctx, wsID, initiativeID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+handoffCols+` FROM handoffs
		WHERE initiative_id=$1 AND workspace_id=$2 ORDER BY version DESC`, initiativeID, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Handoff{}
	for rows.Next() {
		h, err := scanHandoff(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SaveHandoff appends a new version. Nothing is ever updated: the previous
// versions stay as they were. The initiative row is locked for the insert so
// two saves at once get two version numbers.
func (s *Store) SaveHandoff(ctx context.Context, wsID, initiativeID, body, summary, actor string) (models.Handoff, error) {
	summary = strings.Join(strings.Fields(summary), " ")
	if summary == "" {
		return models.Handoff{}, invalidHandoff("summary required")
	}
	if utf8.RuneCountInString(summary) > maxHandoffSummary {
		return models.Handoff{}, invalidHandoff("summary must be at most %d characters", maxHandoffSummary)
	}
	if strings.TrimSpace(body) == "" {
		return models.Handoff{}, invalidHandoff("body required")
	}
	if utf8.RuneCountInString(body) > maxHandoffBody {
		return models.Handoff{}, invalidHandoff("body must be at most %d characters", maxHandoffBody)
	}
	if actor != "ai" {
		actor = "human"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.Handoff{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var lock string
	err = tx.QueryRow(ctx, `SELECT id FROM initiatives WHERE id=$1 AND workspace_id=$2 FOR UPDATE`,
		initiativeID, wsID).Scan(&lock)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Handoff{}, ErrNotFound
	}
	if err != nil {
		return models.Handoff{}, err
	}
	h := models.Handoff{InitiativeID: initiativeID, BodyMD: body, Summary: summary, Actor: actor}
	err = tx.QueryRow(ctx, `
		INSERT INTO handoffs (workspace_id, initiative_id, version, body_md, summary, actor)
		VALUES ($1, $2, (SELECT coalesce(max(version),0)+1 FROM handoffs WHERE initiative_id=$2), $3, $4, $5)
		RETURNING version, created_at`, wsID, initiativeID, body, summary, actor).Scan(&h.Version, &h.CreatedAt)
	if err != nil {
		return models.Handoff{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Handoff{}, err
	}
	return h, nil
}

// FindInitiative resolves an initiative by id or by name (case-insensitive)
// among the given workspaces and says which workspace it is in. A name that
// matches more than one initiative is refused rather than guessed.
func (s *Store) FindInitiative(ctx context.Context, wsIDs []string, ref string) (string, models.Initiative, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(wsIDs) == 0 {
		return "", models.Initiative{}, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`SELECT workspace_id, id FROM initiatives
		 WHERE workspace_id = ANY($1) AND (id::text = $2 OR lower(name) = lower($2))`, wsIDs, ref)
	if err != nil {
		return "", models.Initiative{}, err
	}
	type hit struct{ ws, id string }
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.ws, &h.id); err != nil {
			rows.Close()
			return "", models.Initiative{}, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", models.Initiative{}, err
	}
	switch len(hits) {
	case 0:
		return "", models.Initiative{}, ErrNotFound
	case 1:
		ini, err := s.GetInitiative(ctx, hits[0].ws, hits[0].id)
		return hits[0].ws, ini, err
	}
	return "", models.Initiative{}, invalid("%q matches %d projects; use the id or pass workspace", ref, len(hits))
}

// IssueCategoriesByKeys maps each upper-case issue key found in the workspace
// to the category of its state (completed, canceled, ...), in one query.
// Keys that do not exist are left out.
func (s *Store) IssueCategoriesByKeys(ctx context.Context, wsID string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT upper(i.key), st.category FROM issues i
		JOIN workflow_states st ON st.id = i.state_id
		WHERE i.workspace_id=$1 AND upper(i.key) = ANY($2)`, wsID, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, c string
		if err := rows.Scan(&k, &c); err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, rows.Err()
}
