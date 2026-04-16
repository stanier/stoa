-- +goose Up

CREATE TABLE agentd_agents (
    id                   TEXT        PRIMARY KEY,
    name                 TEXT        NOT NULL UNIQUE,
    display_name         TEXT        NOT NULL DEFAULT '',
    description          TEXT        NOT NULL DEFAULT '',
    system_prompt        TEXT        NOT NULL DEFAULT '',
    allowed_tools        JSONB       NOT NULL DEFAULT '[]',
    mcp_servers          JSONB       NOT NULL DEFAULT '[]',
    model_overrides      JSONB       NOT NULL DEFAULT '{}',
    visibility           TEXT        NOT NULL DEFAULT 'internal'
                             CHECK (visibility IN ('internal','external')),
    owner_user_id        TEXT,
    owner_team           TEXT        NOT NULL DEFAULT '',
    input_content_types  JSONB       NOT NULL DEFAULT '["text/plain"]',
    output_content_types JSONB       NOT NULL DEFAULT '["text/plain"]',
    metadata             JSONB       NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at         TIMESTAMPTZ
);

CREATE INDEX agentd_agents_visibility ON agentd_agents(visibility, published_at);
CREATE INDEX agentd_agents_owner      ON agentd_agents(owner_user_id);

-- +goose Down
DROP TABLE IF EXISTS agentd_agents;
