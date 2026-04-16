-- +goose Up

CREATE TABLE local_users (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username     TEXT        NOT NULL UNIQUE,
    email        TEXT        NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Credentials are separated from users so the user record stays clean
-- and the credential table can be locked/updated independently.
CREATE TABLE local_credentials (
    user_id         UUID        PRIMARY KEY REFERENCES local_users(id) ON DELETE CASCADE,
    password_hash   TEXT        NOT NULL,
    failed_attempts INT         NOT NULL DEFAULT 0,
    locked_at       TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Persisted RS256 signing keypairs. At most one row has active = true.
CREATE TABLE local_signing_keys (
    kid        TEXT        PRIMARY KEY,
    key_pem    TEXT        NOT NULL,   -- PKCS#8 PEM private key
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    active     BOOLEAN     NOT NULL DEFAULT true
);

-- +goose Down
DROP TABLE IF EXISTS local_signing_keys;
DROP TABLE IF EXISTS local_credentials;
DROP TABLE IF EXISTS local_users;
