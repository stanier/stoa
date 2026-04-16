-- +goose Up

-- Maps a Zulip sender email to an authd user_id, established via the !auth flow.
CREATE TABLE zulip_links (
    zulip_email TEXT        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS zulip_links;
