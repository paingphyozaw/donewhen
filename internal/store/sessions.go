package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	// BIZ-282
	SessionGroupKind      = "kind"       // input, output, cache_read, cache_write
	SessionGroupAgentType = "agent_type" // coordinator, implementer, reviewer, …
	SessionGroupAgent     = "agent"      // one row per subagent run
	SessionGroupTicket    = "ticket"     // ticket key × coordinator overhead, split by agent type
)

// AgentTypeCoordinator is the agent type of a session's main transcript.
const AgentTypeCoordinator = "coordinator"

// agentTypeExpr names the agent type of a message (alias m) joined to its run
// (alias a): the coordinator for the main transcript, "unknown" for a run the
// importer sent no meta for.
const agentTypeExpr = `CASE WHEN m.agent_id IS NULL THEN 'coordinator' ELSE coalesce(nullif(a.agent_type, ''), 'unknown') END`

// msgJoin joins each message (alias m) to its session (s) and its run (a).
const msgJoin = `ai_session_messages m JOIN ai_sessions s ON s.id = m.session_id
		LEFT JOIN ai_session_agents a ON a.workspace_id = m.workspace_id AND a.agent_id = m.agent_id`

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
// times of its first and last message, and its ticket keys are the keys of
// its messages' branches.
//
// agents are the session's subagent runs (BIZ-282). A run belongs to the
// session that first sent it; a later push of the same session refreshes its
// type, description and key. A message's ticket key is its run's key when the
// run's description names one, else the key of its branch (msgs[i].IssueKey).
// A message this session already holds gets its agent id and key refreshed
// (an empty agent id keeps the stored one); its tokens and cost never change,
// and a message another session owns is never touched.
func (s *Store) SaveSession(ctx context.Context, wsID string, in models.AgentSession, msgs []models.SessionMessage, agents []models.SessionAgent) (models.AgentSession, error) {
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

	if len(agents) > 0 {
		n := len(agents)
		aid, typ, desc, mdl, key := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		for i, a := range agents {
			aid[i], typ[i], desc[i], mdl[i], key[i] = a.ID, a.Type, a.Description, a.Model, a.IssueKey
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_session_agents AS a (workspace_id, session_id, agent_id, agent_type, description, model, issue_key)
			SELECT $1, $2, x.id, x.t, x.d, x.m, nullif(x.k, '')
			FROM unnest($3::text[], $4::text[], $5::text[], $6::text[], $7::text[]) AS x(id, t, d, m, k)
			ORDER BY x.id
			ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
				agent_type = EXCLUDED.agent_type, description = EXCLUDED.description, issue_key = EXCLUDED.issue_key,
				model = CASE WHEN EXCLUDED.model <> '' THEN EXCLUDED.model ELSE a.model END, updated_at = now()
			WHERE a.session_id = EXCLUDED.session_id`,
			wsID, id, aid, typ, desc, mdl, key); err != nil {
			return models.AgentSession{}, err
		}
	}

	if len(msgs) > 0 {
		n := len(msgs)
		ids, mdl := make([]string, n), make([]string, n)
		at := make([]*time.Time, n)
		branch, key, agent := make([]string, n), make([]*string, n), make([]*string, n)
		inTok, outTok, cr, w5, w1 := make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)
		cost := make([]float64, n)
		for i, m := range msgs {
			ids[i], mdl[i] = m.ID, m.Model
			if !m.At.IsZero() {
				t := m.At
				at[i] = &t
			}
			branch[i] = m.Branch
			if m.IssueKey != "" {
				k := m.IssueKey
				key[i] = &k
			}
			if m.AgentID != "" {
				a := m.AgentID
				agent[i] = &a
			}
			inTok[i], outTok[i], cr[i], w5[i], w1[i] = m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWrite5mTokens, m.CacheWrite1hTokens
			cost[i] = m.CostUSD
		}
		// m.issue_key is the branch key; a run whose description names a key overrides it.
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_session_messages AS t (workspace_id, session_id, message_id, model, at, branch, issue_key, agent_id,
				input_tokens, output_tokens, cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens, cost_usd)
			SELECT $1, $2, m.id, m.model, m.at, m.branch, coalesce(ag.issue_key, m.issue_key), m.agent,
				m.i, m.o, m.cr, m.w5, m.w1, m.cost::numeric(14,6)
			FROM unnest($3::text[], $4::text[], $5::timestamptz[], $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[],
				$10::bigint[], $11::float8[], $12::text[], $13::text[], $14::text[])
				AS m(id, model, at, i, o, cr, w5, w1, cost, branch, issue_key, agent)
			LEFT JOIN ai_session_agents ag ON ag.workspace_id = $1 AND ag.agent_id = m.agent
			ORDER BY m.id -- one lock order for every push, so concurrent pushes with shared ids cannot deadlock
			ON CONFLICT (workspace_id, message_id) DO UPDATE SET
				agent_id = coalesce(EXCLUDED.agent_id, t.agent_id),
				issue_key = coalesce((SELECT r.issue_key FROM ai_session_agents r
					WHERE r.workspace_id = t.workspace_id AND r.agent_id = coalesce(EXCLUDED.agent_id, t.agent_id)), EXCLUDED.issue_key)
			WHERE t.session_id = EXCLUDED.session_id`,
			wsID, id, ids, mdl, at, inTok, outTok, cr, w5, w1, cost, branch, key, agent); err != nil {
			return models.AgentSession{}, err
		}
	}
	if len(agents) > 0 {
		// Messages of a run saved by an earlier push follow the run's key.
		if _, err := tx.Exec(ctx, `
			UPDATE ai_session_messages m SET issue_key = a.issue_key
			FROM ai_session_agents a
			WHERE m.session_id = $1 AND a.workspace_id = m.workspace_id AND a.agent_id = m.agent_id
				AND a.issue_key IS NOT NULL AND m.issue_key IS DISTINCT FROM a.issue_key`, id); err != nil {
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
			started_at = coalesce(m.first_at, s.started_at), ended_at = coalesce(m.last_at, s.ended_at),
			issue_keys = CASE WHEN m.n > 0 THEN m.keys ELSE s.issue_keys END
		FROM (SELECT count(*) AS n, min(at) AS first_at, max(at) AS last_at,
				coalesce(array_agg(DISTINCT issue_key ORDER BY issue_key) FILTER (WHERE issue_key IS NOT NULL), '{}') AS keys
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

// sessionWhere builds the list filter on sessions (alias s); placeholders
// start at n. Time and ticket filters look at the session's messages: a
// session is in range when it holds a message in range (or, holding none,
// when it started in range), and it matches a ticket when one of its messages
// was made on that ticket's branch.
func sessionWhere(f SessionFilter, n int) (string, []any) {
	where := "true"
	var args []any
	next := func(v any) string {
		args = append(args, v)
		n++
		return fmt.Sprintf("$%d", n-1)
	}
	var msgTime, startTime []string
	if !f.From.IsZero() {
		p := next(f.From)
		msgTime = append(msgTime, "coalesce(m.at, s.started_at) >= "+p)
		startTime = append(startTime, "s.started_at >= "+p)
	}
	if !f.Before.IsZero() {
		p := next(f.Before)
		msgTime = append(msgTime, "coalesce(m.at, s.started_at) < "+p)
		startTime = append(startTime, "s.started_at < "+p)
	}
	if len(msgTime) > 0 {
		where += " AND (EXISTS (SELECT 1 FROM ai_session_messages m WHERE m.session_id = s.id AND " +
			strings.Join(msgTime, " AND ") + ") OR (s.messages = 0 AND " + strings.Join(startTime, " AND ") + "))"
	}
	if f.Project != "" {
		where += " AND s.project_path = " + next(f.Project)
	}
	if f.Issue != "" {
		where += " AND EXISTS (SELECT 1 FROM ai_session_messages m WHERE m.session_id = s.id AND m.issue_key = " + next(f.Issue) + ")"
	}
	return where, args
}

// messageWhere builds the summary filter on messages (alias m) and their
// sessions (alias s); placeholders start at n.
func messageWhere(f SessionFilter, n int) (string, []any) {
	where := "true"
	var args []any
	add := func(cond string, v any) {
		where += fmt.Sprintf(" AND "+cond, n)
		args = append(args, v)
		n++
	}
	if !f.From.IsZero() {
		add("coalesce(m.at, s.started_at) >= $%d", f.From)
	}
	if !f.Before.IsZero() {
		add("coalesce(m.at, s.started_at) < $%d", f.Before)
	}
	if f.Project != "" {
		add("s.project_path = $%d", f.Project)
	}
	if f.Issue != "" {
		add("m.issue_key = $%d", f.Issue)
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
		a.Agents = []models.SessionRun{}
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
	if err := urows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Kinds = kindsOfUsage(out[i].Usage)
	}

	// The subagent runs of each session, with the usage of their messages.
	arows, err := s.pool.Query(ctx, `
		SELECT m.session_id, m.agent_id, coalesce(a.agent_type, ''), coalesce(a.description, ''),
			coalesce(a.issue_key, mode() WITHIN GROUP (ORDER BY m.issue_key), ''),
			coalesce(nullif(a.model, ''), mode() WITHIN GROUP (ORDER BY m.model)),
			count(*)::int, sum(m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_5m_tokens + m.cache_write_1h_tokens)::bigint,
			sum(m.cost_usd)::float8
		FROM ai_session_messages m
		LEFT JOIN ai_session_agents a ON a.workspace_id = m.workspace_id AND a.agent_id = m.agent_id
		WHERE m.session_id = ANY($1::uuid[]) AND m.agent_id IS NOT NULL
		GROUP BY m.session_id, m.agent_id, a.agent_type, a.description, a.issue_key, a.model
		ORDER BY sum(m.cost_usd) DESC, m.agent_id`, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var sid string
		var r models.SessionRun
		if err := arows.Scan(&sid, &r.ID, &r.Type, &r.Description, &r.Issue, &r.Model, &r.Messages, &r.Tokens, &r.CostUSD); err != nil {
			return nil, err
		}
		a := &out[index[sid]]
		a.Agents = append(a.Agents, r)
	}
	return out, arows.Err()
}

// kindsOfUsage splits a session's cost by token kind, model by model.
func kindsOfUsage(usage []models.SessionUsage) []models.KindCost {
	var tok [4]int64
	var cost [4]float64
	for _, u := range usage {
		k := models.KindCosts(u.Model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWrite5mTokens, u.CacheWrite1hTokens, u.CostUSD)
		tok[0] += u.InputTokens
		tok[1] += u.OutputTokens
		tok[2] += u.CacheReadTokens
		tok[3] += u.CacheWrite5mTokens + u.CacheWrite1hTokens
		for i := range k {
			cost[i] += k[i]
		}
	}
	out := make([]models.KindCost, len(models.TokenKinds))
	for i, kind := range models.TokenKinds {
		out[i] = models.KindCost{Kind: kind, Tokens: tok[i], CostUSD: cost[i]}
	}
	return out
}

// SessionSummary sums messages per day (UTC, of each message's time),
// project (of its session), model, or ticket key (the key of the message's
// run, else of its branch; "" for none). Every message counts in exactly one
// group, so the groups add up to the real total. sessions counts the distinct
// sessions with messages in the group. The filters apply to messages: from/to
// to their time, issue to their ticket key.
//
// BIZ-282 adds kind (cost split by token kind), agent_type (coordinator and
// each subagent type), agent (one row per subagent run) and ticket (per
// ticket key, split by agent type, with the hub coordinator's overhead apart).
func (s *Store) SessionSummary(ctx context.Context, wsID string, f SessionFilter, group string) ([]models.SessionSummaryRow, error) {
	var key string
	order := "sum(m.cost_usd) DESC, key"
	switch group {
	case SessionGroupDay:
		key = "to_char(coalesce(m.at, s.started_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
		order = "key"
	case SessionGroupProject:
		key = "s.project_path"
	case SessionGroupIssue:
		key = "coalesce(m.issue_key, '')"
	case SessionGroupModel:
		key = "m.model"
	case SessionGroupAgentType:
		return s.summaryByAgentType(ctx, wsID, f)
	case SessionGroupKind:
		return s.summaryByKind(ctx, wsID, f)
	case SessionGroupAgent:
		return s.summaryByAgent(ctx, wsID, f)
	case SessionGroupTicket:
		return s.summaryByTicket(ctx, wsID, f)
	default:
		return nil, invalid("group must be day, project, model, issue, kind, agent_type, agent or ticket")
	}
	where, args := messageWhere(f, 2)
	q := fmt.Sprintf(`
		SELECT %s AS key, count(DISTINCT m.session_id), count(*)::int,
			coalesce(sum(m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_5m_tokens + m.cache_write_1h_tokens), 0)::bigint,
			coalesce(sum(m.cache_read_tokens), 0)::bigint, coalesce(sum(m.cost_usd), 0)::float8
		FROM %s
		WHERE m.workspace_id=$1 AND s.workspace_id=$1 AND %s
		GROUP BY key ORDER BY %s`, key, msgJoin, where, order)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SessionSummaryRow{}
	for rows.Next() {
		var r models.SessionSummaryRow
		if err := rows.Scan(&r.Key, &r.Sessions, &r.Messages, &r.Tokens, &r.CacheReadTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// tokenSum is the SQL sum of a message's four counts.
const tokenSum = "(m.input_tokens + m.output_tokens + m.cache_read_tokens + m.cache_write_5m_tokens + m.cache_write_1h_tokens)"

// summaryByAgentType: one row per agent type. runs is the number of subagent
// runs of the type; for the coordinator it is the number of sessions.
func (s *Store) summaryByAgentType(ctx context.Context, wsID string, f SessionFilter) ([]models.SessionSummaryRow, error) {
	where, args := messageWhere(f, 2)
	q := fmt.Sprintf(`
		SELECT %s AS key, count(DISTINCT m.session_id), count(*)::int,
			count(DISTINCT coalesce(m.agent_id, m.session_id::text))::int,
			coalesce(sum(%s), 0)::bigint, coalesce(sum(m.cache_read_tokens), 0)::bigint, coalesce(sum(m.cost_usd), 0)::float8
		FROM %s
		WHERE m.workspace_id=$1 AND s.workspace_id=$1 AND %s
		GROUP BY key ORDER BY sum(m.cost_usd) DESC, key`, agentTypeExpr, tokenSum, msgJoin, where)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SessionSummaryRow{}
	for rows.Next() {
		var r models.SessionSummaryRow
		if err := rows.Scan(&r.Key, &r.Sessions, &r.Messages, &r.Runs, &r.Tokens, &r.CacheReadTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// summaryByKind splits the cost by token kind. Each model's stored cost is
// split with the price table and scaled to what was stored, so the four rows
// add up to the total cost. No messages → no rows.
func (s *Store) summaryByKind(ctx context.Context, wsID string, f SessionFilter) ([]models.SessionSummaryRow, error) {
	where, args := messageWhere(f, 2)
	q := fmt.Sprintf(`
		SELECT m.model, sum(m.input_tokens)::bigint, sum(m.output_tokens)::bigint, sum(m.cache_read_tokens)::bigint,
			sum(m.cache_write_5m_tokens)::bigint, sum(m.cache_write_1h_tokens)::bigint, sum(m.cost_usd)::float8,
			count(*)::int, array_agg(DISTINCT m.session_id::text)
		FROM %s
		WHERE m.workspace_id=$1 AND s.workspace_id=$1 AND %s
		GROUP BY m.model`, msgJoin, where)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var usage []models.SessionUsage
	sessions := map[string]bool{}
	messages := 0
	for rows.Next() {
		var u models.SessionUsage
		var n int
		var sids []string
		if err := rows.Scan(&u.Model, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens,
			&u.CacheWrite5mTokens, &u.CacheWrite1hTokens, &u.CostUSD, &n, &sids); err != nil {
			return nil, err
		}
		usage = append(usage, u)
		messages += n
		for _, id := range sids {
			sessions[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []models.SessionSummaryRow{}
	if len(usage) == 0 {
		return out, nil
	}
	var cacheRead int64
	for _, u := range usage {
		cacheRead += u.CacheReadTokens
	}
	for _, k := range kindsOfUsage(usage) {
		out = append(out, models.SessionSummaryRow{Key: k.Kind, Sessions: len(sessions), Messages: messages,
			Tokens: k.Tokens, CacheReadTokens: cacheRead, CostUSD: k.CostUSD})
	}
	return out, nil
}

// summaryByAgent: one row per subagent run with messages in the filter. Its
// ticket is the key its description names, else the most common key of its
// messages; its model is the meta's, else its most used model.
func (s *Store) summaryByAgent(ctx context.Context, wsID string, f SessionFilter) ([]models.SessionSummaryRow, error) {
	where, args := messageWhere(f, 2)
	q := fmt.Sprintf(`
		SELECT m.agent_id, min(s.external_id), coalesce(nullif(a.agent_type, ''), 'unknown'), coalesce(a.description, ''),
			coalesce(a.issue_key, mode() WITHIN GROUP (ORDER BY m.issue_key), ''),
			coalesce(nullif(a.model, ''), mode() WITHIN GROUP (ORDER BY m.model)),
			min(coalesce(m.at, s.started_at)), max(coalesce(m.at, s.started_at)),
			count(DISTINCT m.session_id), count(*)::int,
			coalesce(sum(%s), 0)::bigint, coalesce(sum(m.cache_read_tokens), 0)::bigint, coalesce(sum(m.cost_usd), 0)::float8
		FROM %s
		WHERE m.workspace_id=$1 AND s.workspace_id=$1 AND m.agent_id IS NOT NULL AND %s
		GROUP BY m.agent_id, a.agent_type, a.description, a.issue_key, a.model
		ORDER BY sum(m.cost_usd) DESC, m.agent_id`, tokenSum, msgJoin, where)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SessionSummaryRow{}
	for rows.Next() {
		var r models.SessionSummaryRow
		var start, end time.Time
		if err := rows.Scan(&r.Key, &r.Session, &r.Type, &r.Description, &r.Issue, &r.Model, &start, &end,
			&r.Sessions, &r.Messages, &r.Tokens, &r.CacheReadTokens, &r.CostUSD); err != nil {
			return nil, err
		}
		r.StartedAt, r.EndedAt = &start, &end
		r.Runs = 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// coordinatorExpr marks hub coordinator overhead: a message whose key comes
// only from its branch (the coordinator's own, or a run whose description
// names no key) in a session that runs agents for another ticket. Such a
// session is a hub coordinator: its branch is where it sits, not what it
// works on. A session opened on a ticket's own branch stays that ticket's work.
const coordinatorExpr = `((m.agent_id IS NULL OR a.issue_key IS NULL) AND EXISTS (
	SELECT 1 FROM ai_session_agents h
	WHERE h.session_id = m.session_id AND h.issue_key IS NOT NULL AND h.issue_key <> coalesce(m.issue_key, '')))`

// summaryByTicket: one row per ticket key, and one more per key that hub
// coordinators sat on (Coordinator true). Each row is split by agent type.
func (s *Store) summaryByTicket(ctx context.Context, wsID string, f SessionFilter) ([]models.SessionSummaryRow, error) {
	where, args := messageWhere(f, 2)
	q := fmt.Sprintf(`
		SELECT coalesce(m.issue_key, '') AS key, %s AS coord, %s AS type,
			array_agg(DISTINCT m.session_id::text), count(*)::int, count(DISTINCT m.agent_id)::int,
			coalesce(sum(%s), 0)::bigint, coalesce(sum(m.cache_read_tokens), 0)::bigint, coalesce(sum(m.cost_usd), 0)::float8,
			max(coalesce(m.at, s.started_at))
		FROM %s
		WHERE m.workspace_id=$1 AND s.workspace_id=$1 AND %s
		GROUP BY key, coord, type`, coordinatorExpr, agentTypeExpr, tokenSum, msgJoin, where)
	rows, err := s.pool.Query(ctx, q, append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type rowKey struct {
		key   string
		coord bool
	}
	byKey := map[rowKey]*models.SessionSummaryRow{}
	sessions := map[rowKey]map[string]bool{}
	var order []rowKey
	for rows.Next() {
		var k rowKey
		var typ string
		var sids []string
		var n, runs int
		var tok, cr int64
		var cost float64
		var last time.Time
		if err := rows.Scan(&k.key, &k.coord, &typ, &sids, &n, &runs, &tok, &cr, &cost, &last); err != nil {
			return nil, err
		}
		r, ok := byKey[k]
		if !ok {
			r = &models.SessionSummaryRow{Key: k.key, Coordinator: k.coord,
				CostByType: map[string]float64{}, RunsByType: map[string]int{}}
			byKey[k], sessions[k] = r, map[string]bool{}
			order = append(order, k)
		}
		for _, id := range sids {
			sessions[k][id] = true
		}
		r.Messages += n
		r.Tokens += tok
		r.CacheReadTokens += cr
		r.CostUSD += cost
		r.CostByType[typ] += cost
		if typ != AgentTypeCoordinator {
			r.RunsByType[typ] += runs
			r.Runs += runs
		}
		if r.LastAt == nil || last.After(*r.LastAt) {
			l := last
			r.LastAt = &l
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]models.SessionSummaryRow, 0, len(order))
	for _, k := range order {
		r := byKey[k]
		r.Sessions = len(sessions[k])
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return !out[i].Coordinator
	})
	return out, nil
}
