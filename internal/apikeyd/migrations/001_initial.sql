-- +goose Up
CREATE TABLE api_keys (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    key_hash     TEXT        NOT NULL UNIQUE,  -- SHA-256 hex of raw key
    prefix       TEXT        NOT NULL,         -- first 8 chars of raw key (display only)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,                  -- NULL = never expires
    revoked_at   TIMESTAMPTZ                   -- NULL = active
);

CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);
CREATE INDEX api_keys_key_hash_idx ON api_keys (key_hash);

-- +goose Down
DROP TABLE api_keys;
