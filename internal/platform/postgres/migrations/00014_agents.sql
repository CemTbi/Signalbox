-- Agents: `signalbox agent` processes on people's machines, which send
-- their owner's requests from there. Owned by internal/modules/agents.
-- See docs/adr/0037-local-agent.md.

-- +goose Up
CREATE TABLE agents (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The name its owner picks it by, such as the machine's name.
    name         text NOT NULL,
    hostname     text NOT NULL DEFAULT '',
    os           text NOT NULL DEFAULT '',
    version      text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agents_user_id_name_key UNIQUE (user_id, name),
    CONSTRAINT agents_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT agents_details_length CHECK (char_length(hostname) <= 255 AND char_length(os) <= 64 AND char_length(version) <= 64)
);

-- A run through an agent says which.
ALTER TABLE test_runs
    ADD COLUMN agent_id uuid REFERENCES agents (id) ON DELETE SET NULL,
    ADD COLUMN agent_name text NOT NULL DEFAULT '';
CREATE INDEX test_runs_agent_id_idx ON test_runs (agent_id);

-- +goose Down
DROP INDEX test_runs_agent_id_idx;
ALTER TABLE test_runs DROP COLUMN agent_name, DROP COLUMN agent_id;
DROP TABLE agents;
