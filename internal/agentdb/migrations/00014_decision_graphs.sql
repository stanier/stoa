-- +goose Up

-- Decision graphs built on the web UI's Decision graphs screen. graph holds
-- the editor's document ({root, nodes}) as the browser wrote it; agentd only
-- checks that it is a JSON object of bounded size.
CREATE TABLE agentd_decision_graphs (
    id         TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    graph      JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agentd_decision_graphs_user ON agentd_decision_graphs(user_id, updated_at DESC);

-- +goose Down
DROP TABLE IF EXISTS agentd_decision_graphs;
