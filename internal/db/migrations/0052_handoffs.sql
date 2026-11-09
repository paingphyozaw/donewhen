-- Handoff per project (BIZ-280): the note one session leaves for the next.
-- Append-only: a save inserts a new version, the latest row is the handoff and
-- older rows are the history. Rows are never updated.
CREATE TABLE handoffs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    initiative_id uuid NOT NULL REFERENCES initiatives(id) ON DELETE CASCADE,
    version       int  NOT NULL CHECK (version >= 1),
    body_md       text NOT NULL CHECK (char_length(body_md) BETWEEN 1 AND 200000),
    summary       text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 200),
    actor         text NOT NULL CHECK (actor IN ('human', 'ai')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (initiative_id, version)
);
