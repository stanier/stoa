-- +goose Up
ALTER TABLE agentd_agents ADD COLUMN external_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agentd_agents DROP COLUMN external_url;
