-- +goose Up
CREATE TABLE user_mcp_server_prefs (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_name TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, server_name)
);
CREATE TABLE user_mcp_tool_prefs (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    enabled   BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, tool_name)
);

-- +goose Down
DROP TABLE IF EXISTS user_mcp_tool_prefs;
DROP TABLE IF EXISTS user_mcp_server_prefs;
