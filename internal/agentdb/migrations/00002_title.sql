-- +goose Up
ALTER TABLE agentd_sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agentd_sessions DROP COLUMN title;
