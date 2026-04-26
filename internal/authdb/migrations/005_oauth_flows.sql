-- +goose Up
CREATE TABLE oauth_flows (
    state            TEXT        PRIMARY KEY,
    provider_id      TEXT        NOT NULL,
    code_verifier    TEXT        NOT NULL,
    login_id         TEXT        NOT NULL DEFAULT '',
    zulip_link_token TEXT        NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ NOT NULL
);

CREATE TABLE login_flows (
    id                  TEXT        PRIMARY KEY,
    provider_id         TEXT        NOT NULL,
    auth_url            TEXT        NOT NULL,
    post_login_redirect TEXT        NOT NULL DEFAULT '',
    session_token       TEXT        NOT NULL DEFAULT '',
    llm_key             TEXT        NOT NULL DEFAULT '',
    err_msg             TEXT        NOT NULL DEFAULT '',
    expires_at          TIMESTAMPTZ NOT NULL
);

CREATE TABLE zulip_link_tokens (
    token        TEXT        PRIMARY KEY,
    zulip_email  TEXT        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS zulip_link_tokens;
DROP TABLE IF EXISTS login_flows;
DROP TABLE IF EXISTS oauth_flows;
