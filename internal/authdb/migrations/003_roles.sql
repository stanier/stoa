-- +goose Up

-- roles: named roles that can be assigned to users.
-- persona and allowed_tools mirror the agentd SessionConfig fields.
CREATE TABLE roles (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL UNIQUE,
    persona       TEXT        NOT NULL DEFAULT '',
    allowed_tools JSONB       NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- user_roles: assigns exactly one role per user.
-- ON DELETE CASCADE: removing a user also removes their role assignment.
-- ON DELETE RESTRICT on role_id: a role cannot be dropped while users are assigned to it.
CREATE TABLE user_roles (
    user_id     UUID        PRIMARY KEY REFERENCES users(id)  ON DELETE CASCADE,
    role_id     UUID        NOT NULL    REFERENCES roles(id)  ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX user_roles_role_id_idx ON user_roles(role_id);

-- +goose Down
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
