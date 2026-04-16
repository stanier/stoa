-- +goose Up
-- +goose StatementBegin

CREATE TABLE users (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id  TEXT        NOT NULL,          -- issuer URL (stable provider identifier)
    sub          TEXT        NOT NULL,          -- provider's stable subject identifier
    email        TEXT,
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ,
    UNIQUE (provider_id, sub)                   -- 1-to-1 mapping, no cross-provider merge
);

CREATE TABLE sessions (
    token_hash   TEXT        PRIMARY KEY,       -- SHA-256 hex of raw token; plaintext never stored
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
