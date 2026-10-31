-- How far each of the telemetry module's periodic jobs has got, such as
-- building the service graph a minute at a time. A job claims its next
-- step by locking its row, so processes that run it at once take turns
-- and never repeat a step. Owned by internal/modules/telemetry.
-- See docs/adr/0044-service-graph.md.

-- +goose Up
CREATE TABLE telemetry_jobs (
    name         text PRIMARY KEY,
    -- The end of the last step done: for the service graph, the end of the
    -- last minute built.
    done_through timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE telemetry_jobs;
