-- BIZ-282: who made each message. One row per subagent run of a Claude Code
-- session, from the run's agent-<id>.meta.json. description is the short task
-- title the coordinator gave the run ("Build BIZ-278 sessions backend"); it is
-- the only free text stored. issue_key is the ticket key that description
-- names, if any. Additive only: nothing is dropped or rewritten.
CREATE TABLE ai_session_agents (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    session_id   uuid NOT NULL REFERENCES ai_sessions(id) ON DELETE CASCADE,
    agent_id     text NOT NULL CHECK (char_length(agent_id) BETWEEN 1 AND 200),
    agent_type   text NOT NULL DEFAULT '' CHECK (char_length(agent_type) <= 100),
    description  text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
    model        text NOT NULL DEFAULT '' CHECK (char_length(model) <= 100),
    issue_key    text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, agent_id)
);
CREATE INDEX ai_session_agents_session_idx ON ai_session_agents (session_id);

-- The subagent run that made the message; null means the coordinator (the
-- session's main transcript).
ALTER TABLE ai_session_messages ADD COLUMN agent_id text;
-- The ticket key the message's branch names ('' for none); null for a message
-- saved before 0053 and not pushed since. issue_key is the run's key, else this.
ALTER TABLE ai_session_messages ADD COLUMN branch_key text;
CREATE INDEX ai_session_messages_agent_idx ON ai_session_messages (workspace_id, agent_id);
