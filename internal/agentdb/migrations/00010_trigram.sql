-- +goose Up

-- Enable the pg_trgm extension for trigram-based similarity search.
-- This supports prefix/partial-word matching that plainto_tsquery cannot do.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Trigram GIN index on session titles for fast similarity queries.
CREATE INDEX IF NOT EXISTS agentd_sessions_title_trgm
    ON agentd_sessions USING GIN (title gin_trgm_ops);

-- Trigram GIN index on message content for fast similarity queries.
CREATE INDEX IF NOT EXISTS agentd_messages_content_trgm
    ON agentd_messages USING GIN (content gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS agentd_messages_content_trgm;
DROP INDEX IF EXISTS agentd_sessions_title_trgm;
DROP EXTENSION IF EXISTS pg_trgm;
