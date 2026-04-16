-- +goose Up
CREATE TABLE IF NOT EXISTS token_usage (
    id            BIGSERIAL PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    session_id    TEXT        NOT NULL,
    provider      TEXT        NOT NULL DEFAULT '',
    model         TEXT        NOT NULL DEFAULT '',
    input_tokens  INT         NOT NULL DEFAULT 0,
    output_tokens INT         NOT NULL DEFAULT 0,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS token_usage_user_id_idx ON token_usage (user_id, recorded_at DESC);

CREATE TABLE IF NOT EXISTS token_budgets (
    user_id              TEXT PRIMARY KEY,
    daily_input_limit    BIGINT,
    daily_output_limit   BIGINT,
    monthly_input_limit  BIGINT,
    monthly_output_limit BIGINT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS token_budgets;
DROP INDEX IF EXISTS token_usage_user_id_idx;
DROP TABLE IF EXISTS token_usage;
