-- +goose Up

CREATE TABLE agentd_sessions (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT,                           -- authd user UUID; NULL for service/anonymous
    persona             TEXT        NOT NULL DEFAULT '',
    source              TEXT        NOT NULL DEFAULT '', -- "web", "tui", "zulip", "cli", etc.
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_active_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_input_tokens   INT         NOT NULL DEFAULT 0,
    total_output_tokens INT         NOT NULL DEFAULT 0
);

CREATE INDEX agentd_sessions_user_created  ON agentd_sessions(user_id, created_at DESC);
CREATE INDEX agentd_sessions_source        ON agentd_sessions(source, created_at DESC);
CREATE INDEX agentd_sessions_last_active   ON agentd_sessions(last_active_at DESC);

CREATE TABLE agentd_messages (
    id           BIGSERIAL   PRIMARY KEY,
    session_id   TEXT        NOT NULL REFERENCES agentd_sessions(id) ON DELETE CASCADE,
    seq          INT         NOT NULL,  -- 0-indexed position within session (non-system messages only)
    role         TEXT        NOT NULL,
    content      TEXT        NOT NULL,
    tool_name    TEXT        NOT NULL DEFAULT '',
    tool_use_id  TEXT        NOT NULL DEFAULT '',
    tool_success BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, seq)
);

CREATE INDEX agentd_messages_session ON agentd_messages(session_id, seq);

-- +goose Down
DROP TABLE IF EXISTS agentd_messages;
DROP TABLE IF EXISTS agentd_sessions;
