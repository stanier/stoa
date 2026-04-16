-- +goose Up

-- Full-text search index on session titles.
CREATE INDEX IF NOT EXISTS agentd_sessions_title_fts
    ON agentd_sessions USING GIN (to_tsvector('english', title));

-- Full-text search index on message content.
CREATE INDEX IF NOT EXISTS agentd_messages_content_fts
    ON agentd_messages USING GIN (to_tsvector('english', content));

-- +goose Down
DROP INDEX IF EXISTS agentd_messages_content_fts;
DROP INDEX IF EXISTS agentd_sessions_title_fts;
