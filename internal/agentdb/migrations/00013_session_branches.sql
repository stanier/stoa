-- +goose Up

ALTER TABLE agentd_sessions
    ADD COLUMN parent_session_id TEXT REFERENCES agentd_sessions(id) ON DELETE SET NULL,
    ADD COLUMN branch_point      INT;  -- seq index of the last message copied into this branch

CREATE INDEX agentd_sessions_parent ON agentd_sessions(parent_session_id)
    WHERE parent_session_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS agentd_sessions_parent;
ALTER TABLE agentd_sessions
    DROP COLUMN IF EXISTS branch_point,
    DROP COLUMN IF EXISTS parent_session_id;
