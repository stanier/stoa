-- +goose Up

-- Bind each browser login to the client that started it.
--   * Web (post_login_redirect) flows: authd redirects back with a one-time
--     exchange code instead of the session token; the token is only released
--     to POST /oauth/login/exchange with that code.
--   * Terminal (polling) flows: after the IdP login, the user must confirm the
--     sign-in in the browser before the token is issued. confirm_user_id holds
--     the authenticated user until then.
ALTER TABLE login_flows
    ADD COLUMN exchange_code_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN confirm_user_id    TEXT NOT NULL DEFAULT '',
    ADD COLUMN confirm_nonce_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE login_flows
    DROP COLUMN IF EXISTS confirm_nonce_hash,
    DROP COLUMN IF EXISTS confirm_user_id,
    DROP COLUMN IF EXISTS exchange_code_hash;
