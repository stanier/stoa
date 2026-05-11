-- +goose Up

CREATE TABLE agentd_projects (
    id            TEXT        PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    color         TEXT        NOT NULL DEFAULT '#a89a8a',
    glyph         TEXT        NOT NULL DEFAULT '○',
    emoji         TEXT        NOT NULL DEFAULT '📁',
    instructions  TEXT        NOT NULL DEFAULT '',
    default_model TEXT        NOT NULL DEFAULT '',
    default_tools JSONB       NOT NULL DEFAULT '[]',
    files         JSONB       NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agentd_projects_user ON agentd_projects(user_id, created_at DESC);

ALTER TABLE agentd_sessions
    ADD COLUMN project_id TEXT REFERENCES agentd_projects(id) ON DELETE SET NULL;

CREATE INDEX agentd_sessions_project ON agentd_sessions(project_id);

-- +goose Down
DROP INDEX IF EXISTS agentd_sessions_project;
ALTER TABLE agentd_sessions DROP COLUMN IF EXISTS project_id;
DROP TABLE IF EXISTS agentd_projects;
