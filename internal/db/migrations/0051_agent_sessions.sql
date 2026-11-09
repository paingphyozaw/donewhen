-- BIZ-278: Claude Code sessions and their token usage. Numbers and metadata
-- only: no message text is ever stored here.
-- The tables are not called agent_sessions: 0036 drops a legacy table of that
-- name, and a re-run of 0036 must never drop this data.
CREATE TABLE ai_sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    external_id  text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 200),
    project_path text NOT NULL DEFAULT '',
    git_branch   text NOT NULL DEFAULT '',
    issue_keys   text[] NOT NULL DEFAULT '{}',
    started_at   timestamptz NOT NULL,
    ended_at     timestamptz NOT NULL,
    messages     int NOT NULL DEFAULT 0 CHECK (messages >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, external_id),
    CHECK (ended_at >= started_at)
);
CREATE INDEX ai_sessions_started_idx ON ai_sessions (workspace_id, started_at DESC);
CREATE INDEX ai_sessions_issue_keys_idx ON ai_sessions USING gin (issue_keys);

-- One row per session and model. cost_usd is fixed when the session is saved,
-- so a later price change does not rewrite old sessions.
CREATE TABLE ai_session_usage (
    session_id         uuid NOT NULL REFERENCES ai_sessions(id) ON DELETE CASCADE,
    model              text NOT NULL CHECK (char_length(model) BETWEEN 1 AND 100),
    input_tokens       bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens      bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens  bigint NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens bigint NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    cost_usd           numeric(12,4) NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, model)
);
