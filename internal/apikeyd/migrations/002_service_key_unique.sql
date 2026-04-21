-- +goose Up
-- Prevent duplicate active service keys per (user_id, name).
-- The admin provisioning endpoint relies on this to safely revoke-and-recreate.
CREATE UNIQUE INDEX api_keys_active_name_uniq
    ON api_keys (user_id, name)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX api_keys_active_name_uniq;
