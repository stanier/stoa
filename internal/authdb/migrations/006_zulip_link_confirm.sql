-- +goose Up

-- A Zulip link is only stored after the signed-in user confirms it. Between
-- the OIDC callback and that confirmation, the pending link lives in
-- zulip_link_tokens with the authenticated user_id set (link tokens handed
-- out by /link/zulip/start keep user_id = '').
ALTER TABLE zulip_link_tokens ADD COLUMN user_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE zulip_link_tokens DROP COLUMN IF EXISTS user_id;
