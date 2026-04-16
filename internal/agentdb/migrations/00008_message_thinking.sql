-- +goose Up
ALTER TABLE agentd_messages ADD COLUMN thinking TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agentd_messages DROP COLUMN thinking;
