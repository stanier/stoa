-- +goose Up
-- A session's tool restrictions must survive an agentd restart. NULL
-- allowed_tools means the persona's full tool set, as in memory.
ALTER TABLE agentd_sessions
    ADD COLUMN allowed_tools TEXT[],
    ADD COLUMN tools_locked  BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE agentd_sessions
    DROP COLUMN IF EXISTS tools_locked,
    DROP COLUMN IF EXISTS allowed_tools;
