-- Runs of a collection or a folder as a test suite, with every step's
-- results, secrets masked. Kept 90 days, and at most the newest 100 runs of
-- each collection. Owned by internal/modules/testruns.
-- See docs/adr/0033-collection-runs.md.

-- +goose Up
CREATE TABLE test_runs (
    id               uuid PRIMARY KEY,
    project_id       uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    collection_id    uuid NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    -- The folder run, or null for the whole collection.
    folder_id        uuid REFERENCES collection_items (id) ON DELETE SET NULL,
    environment_id   uuid REFERENCES environments (id) ON DELETE SET NULL,
    started_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    -- The collection's or folder's name when the run started.
    name             text NOT NULL,
    environment_name text NOT NULL DEFAULT '',
    status           text NOT NULL,
    -- Iterations, data rows, delay and bail.
    options          jsonb NOT NULL,
    -- Why the run ended early: bail, steps, a time limit or an error.
    stopped          text NOT NULL DEFAULT '',
    cancel_requested boolean NOT NULL DEFAULT false,
    -- Updated while the process running it is alive; a stale one means
    -- that process stopped.
    heartbeat_at     timestamptz NOT NULL DEFAULT now(),
    requests         integer NOT NULL DEFAULT 0,
    failed_requests  integer NOT NULL DEFAULT 0,
    tests            integer NOT NULL DEFAULT 0,
    failed_tests     integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz,
    CONSTRAINT test_runs_status CHECK (status IN ('queued', 'running', 'passed', 'failed', 'canceled', 'interrupted'))
);

-- A collection's runs, newest first; IDs are UUIDv7.
CREATE INDEX test_runs_collection_idx ON test_runs (collection_id, id DESC);
CREATE INDEX test_runs_project_id_idx ON test_runs (project_id);
CREATE INDEX test_runs_folder_id_idx ON test_runs (folder_id);
CREATE INDEX test_runs_environment_id_idx ON test_runs (environment_id);
CREATE INDEX test_runs_started_by_idx ON test_runs (started_by);
CREATE INDEX test_runs_created_at_idx ON test_runs (created_at);
CREATE INDEX test_runs_unfinished_idx ON test_runs (status) WHERE status IN ('queued', 'running');

CREATE TABLE test_run_steps (
    run_id        uuid NOT NULL REFERENCES test_runs (id) ON DELETE CASCADE,
    -- The step's place in the run, from 1.
    seq           integer NOT NULL,
    iteration     integer NOT NULL,
    item_id       uuid,
    name          text NOT NULL,
    path          text NOT NULL,
    method        text NOT NULL,
    url           text NOT NULL,
    -- Null when the request got no response.
    status        integer,
    failed        boolean NOT NULL,
    duration_us   bigint NOT NULL,
    -- The request's headers, the response's metadata, the failure if any,
    -- tests, script errors, console output and variable changes.
    record        jsonb NOT NULL,
    -- The first 16 KiB of the response body, kept when the step failed.
    response_body bytea,
    PRIMARY KEY (run_id, seq),
    CONSTRAINT test_run_steps_body_size CHECK (octet_length(response_body) <= 16384)
);

-- +goose Down
DROP TABLE test_run_steps;
DROP TABLE test_runs;
