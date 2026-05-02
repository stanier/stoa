-- +goose Up
ALTER TABLE agentd_sessions ADD COLUMN llm_overrides JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE agentd_sessions DROP COLUMN IF EXISTS llm_overrides;
