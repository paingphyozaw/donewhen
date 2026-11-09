package store

import (
	"context"
	"fmt"
	"time"

	"github.com/johnreginald/donewhen/internal/models"
)

// SessionFilter narrows a session list or summary. Zero values do not filter.
type SessionFilter struct {
	From    time.Time // started_at >= From
	Before  time.Time // started_at < Before
	Project string    // exact project path
	Issue   string    // sessions whose issue keys hold this key
	Limit   int       // list only; 0 means the default
}

// Summary groups accepted by SessionSummary.
const (
	SessionGroupDay     = "day"
	SessionGroupProject = "project"
	SessionGroupModel   = "model"
	SessionGroupIssue   = "issue"
)

const (
	defaultSessionLimit = 100
	maxSessionLimit     = 500
)

// SaveSession adds one push of a session: it merges the session's metadata,
// adds the messages no session of the workspace holds yet, then rebuilds the
// session's usage rows, message count and time range from the messages it
// holds. A message id another session (or an earlier push) already sent is
// skipped, so a push is additive and sending it again changes nothing. The
// caller sets each message's cost.
//
// Metadata merge: the earliest start and latest end, the union of ticket
// keys, the first non-empty project path, and the branch of the push whose
// end is latest. Once the session holds messages, its start and end are the
// times of its first and last message.
func (s *Store) SaveSession(ctx context.Context, wsID string, in models.AgentSession, msgs []models.SessionMessage) (models.AgentSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.AgentSession{}, err
	}
	defer tx.Rollback(ctx)

	keys := in.IssueKeys
	if keys == nil {
		keys = []string{}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO ai_sessions AS s (workspace_id, external_id, project_path, git_branch, issue_keys, started_at, ended_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (workspace_id, external_id) DO UPDATE SET
			project_path = CASE WHEN s.project_path = '' THEN EXCLUDED.project_path ELSE s.project_path END,
			git_branch = CASE WHEN EXCLUDED.git_branch <> '' AND (s.git_branch = '' OR EXCLUDED.ended_at >= s.ended_at)
				THEN EXCLUDED.git_branch ELSE s.git_branch END,
			issue_keys = ARRAY(SELECT DISTINCT k FROM unnest(s.issue_keys || EXCLUDED.issue_keys) AS k ORDER BY k),
			started_at = LEAST(s.started_at, EXCLUDED.started_at),
			ended_at = GREATEST(s.ended_at, EXCLUDED.ended_at),
			updated_at = now()
		RETURNING id`,
		wsID, in.ExternalID, in.ProjectPath, in.GitBranch, keys, in.StartedAt, in.EndedAt,
	).Scan(&id)
	if err != nil {
		return models.AgentSession{}, err
	}

	if len(msgs) > 0 {
		n := len(msgs)
		ids, mdl := make([]string, n), make([]string, n)
		at := make([]*time.Time, n)
		inTok, outTok, cr, w5, w1 := make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)
		cost := make([]float64, n)
		for i, m := range msgs {
			ids[i], mdl[i] = m.ID, m.Model
			if !m.At.IsZero() {
				t := m.At
				at[i] = &t
			}
			inTok[i], outTok[i], cr[i], w5[i], w1[i] = m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWrite5mTokens, m.CacheWrite1hTokens
			cost[i] = m.CostUSD
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_session_messages (workspace_id, session_id, message_id, model, at, input_tokens, output_tokens,
				cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens, cost_usd)
			SELECT $1, $2, m.id, m.model, m.at, m.i, m.o, m.cr, m.w5, m.w1, m.cost::numeric(14,6)
			FROM unnest($3::text[], $4::text[], $5::timestamptz[], $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[],
				$10::bigint[], $11::float8[]) AS m(id, model, at, i, o, cr, w5, w1, cost)
			ON CONFLICT (workspace_id, message_id) DO NOTHING`,
			wsID, id, ids, mdl, at, inTok, outTok, cr, w5, w1, cost); err != nil {
			return models.AgentSession{}, err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM ai_session_usage WHERE session_id=$1`, id); err != nil {
		return models.AgentSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_session_usage (session_id, model, input_tokens, output_tokens, cache_read_tokens,
			cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens, cost_usd)
		SELECT session_id, model, sum(input_tokens), sum(output_tokens), sum(cache_read_tokens),
			sum(cache_write_5m_tokens + cache_write_1h_tokens), sum(cache_write_5m_tokens), sum(cache_write_1h_tokens), sum(cost_usd)
		FROM ai_session_messages WHERE session_id=$1 GROUP BY session_id, model`, id); err != nil {
		return models.AgentSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_sessions s SET messages = m.n,
			started_at = coalesce(m.first_at, s.started_at), ended_at = coalesce(m.last_at, s.ended_at)
		FROM (SELECT count(*) AS n, min(at) AS first_at, max(at) AS last_at
			FROM ai_session_messages WHERE session_id=$1) m
		WHERE s.id=$1`, id); err != nil {
		return models.AgentSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.AgentSession{}, err
	}
	return s.GetSession(ctx, wsID, in.ExternalID)
}

// GetSession returns one session of the workspace by its external id.
func (s *Store) GetSession(ctx context.Context, wsID, externalID string) (models.AgentSession, error) {
	list, err := s.listSessions(ctx, wsID, `s.external_id=$2`, []any{externalID}, 1)
	if err != nil {
		return models.AgentSession{}, err
	}
	if len(list) == 0 {
		return models.AgentSession{}, ErrNotFound
	}
	return list[0], nil
}

// ListSessions returns the workspace's sessions, newest first, each with its
// usage rows and totals.
func (s *Store) ListSessions(ctx context.Context, wsID string, f SessionFilter) ([]models.AgentSession, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultSessionLimit
	}
	if limit > maxSessionLimit {
		limit = maxSessionLimit
	}
	where, args := sessionWhere(f, 2)
	return s.listSessions(ctx, wsID, where, args, limit)
}

// sessionWhere builds the filter conditions on alias s; placeholders start at n.
func sessionWhere(f SessionFilter, n int) (string, []any) {
	where := "true"
	var args []any
	add := func(cond string, v any) {
		where += fmt.Sprintf(" AND "+cond, n)
		args = append(args, v)
		n++
	}
	if !f.From.IsZero() {
		add("s.started_at >= $%d", f.From)
	}
	if !f.Before.IsZero() {
		add("s.started_at < $%d", f.Before)
	}
	if f.Project != "" {
		add("s.project_path = $%d", f.Project)
	}
	if f.Issue != "" {
		add("$%d = ANY(s.issue_keys)", f.Issue)
	}
	return where, args
}

func (s *Store) listSessions(ctx context.Context, wsID, where string, args []any, limit int) ([]models.AgentSession, error) {
	q := fmt.Sprintf(`
		SELECT s.id, s.external_id, s.project_path, s.git_branch, s.issue_keys, s.started_at, s.ended_at, s.messages
		FROM ai_sessions s
		WHERE s.workspace_id=$1 AND %s
		ORDER BY s.started_at DESC, s.external_id
		LIMIT %d`, where, limit)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AgentSession{}
	index := map[string]int{}
	var ids []string
	for rows.Next() {
		var a models.AgentSession
		if err := rows.Scan(&a.ID, &a.ExternalID, &a.ProjectPath, &a.GitBranch, &a.IssueKeys,
			&a.StartedAt, &a.EndedAt, &a.Messages); err != nil {
			return nil, err
		}
		if a.IssueKeys == nil {
			a.IssueKeys = []string{}
		}
		a.Usage = []models.SessionUsage{}
		index[a.ID] = len(out)
		ids = append(ids, a.ID)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	urows, err := s.pool.Query(ctx, `
		SELECT session_id, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			cache_write_5m_tokens, cache_write_1h_tokens, cost_usd::float8
		FROM ai_session_usage WHERE session_id = ANY($1::uuid[]) ORDER BY cost_usd DESC, model`, ids)
	if err != nil {
		return nil, err
	}
	defer urows.Close()
	for urows.Next() {
		var sid string
		var u models.SessionUsage
		if err := urows.Scan(&sid, &u.Model, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens,
			&u.CacheWriteTokens, &u.CacheWrite5mTokens, &u.CacheWrite1hTokens, &u.CostUSD); err != nil {
			return nil, err
		}
		a := &out[index[sid]]
		a.Usage = append(a.Usage, u)
		a.Tokens += u.Tokens()
		a.CostUSD += u.CostUSD
	}
	return out, urows.Err()
}

// SessionSummary sums sessions per day (UTC), project, model or ticket key.
// For a model, sessions counts the sessions that used it and the sums are that
// model's rows only. For a ticket, a session counts under every key it holds;
// sessions with no key are grouped under "".
func (s *Store) SessionSummary(ctx context.Context, wsID string, f SessionFilter, group string) ([]models.SessionSummaryRow, error) {
	where, args := sessionWhere(f, 2)
	var q string
	totals := fmt.Sprintf(`
		WITH t AS (
			SELECT s.id, s.started_at, s.project_path, s.issue_keys,
				coalesce(sum(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens), 0) AS tokens,
				coalesce(sum(u.cost_usd), 0) AS cost
			FROM ai_sessions s LEFT JOIN ai_session_usage u ON u.session_id = s.id
			WHERE s.workspace_id=$1 AND %s
			GROUP BY s.id)`, where)
	switch group {
	case SessionGroupDay:
		q = totals + `
		SELECT to_char(started_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS key, count(*), sum(tokens)::bigint, sum(cost)::float8
		FROM t GROUP BY key ORDER BY key`
	case SessionGroupProject:
		q = totals + `
		SELECT project_path AS key, count(*), sum(tokens)::bigint, sum(cost)::float8
		FROM t GROUP BY key ORDER BY sum(cost) DESC, key`
	case SessionGroupIssue:
		q = totals + `
		SELECT k AS key, count(*), sum(tokens)::bigint, sum(cost)::float8
		FROM t CROSS JOIN LATERAL unnest(CASE WHEN cardinality(issue_keys) = 0 THEN ARRAY[''] ELSE issue_keys END) AS k
		GROUP BY key ORDER BY sum(cost) DESC, key`
	case SessionGroupModel:
		q = fmt.Sprintf(`
		SELECT u.model AS key, count(DISTINCT s.id),
			sum(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens)::bigint,
			sum(u.cost_usd)::float8
		FROM ai_sessions s JOIN ai_session_usage u ON u.session_id = s.id
		WHERE s.workspace_id=$1 AND %s
		GROUP BY u.model ORDER BY sum(u.cost_usd) DESC, key`, where)
	default:
		return nil, invalid("group must be day, project, model or issue")
	}
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SessionSummaryRow{}
	for rows.Next() {
		var r models.SessionSummaryRow
		if err := rows.Scan(&r.Key, &r.Sessions, &r.Tokens, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
