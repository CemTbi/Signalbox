-- Incidents: a monitor going down opens one, and it resolves when the
-- monitor is up again or someone resolves it. Its timeline records every
-- state change, acknowledgement and resolution. Owned by
-- internal/modules/monitors. See docs/adr/0048-incidents.md.

-- +goose Up
CREATE TABLE incidents (
    id                     uuid PRIMARY KEY,
    project_id             uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- Null once the monitor is deleted; its name stays.
    monitor_id             uuid REFERENCES monitors (id) ON DELETE SET NULL,
    environment_id         uuid REFERENCES environments (id) ON DELETE SET NULL,
    monitor_name           text NOT NULL,
    state                  text NOT NULL DEFAULT 'open',
    opened_at              timestamptz NOT NULL,
    -- The check that took the monitor down.
    first_check_at         timestamptz NOT NULL,
    first_status           integer,
    first_failure_code     text NOT NULL DEFAULT '',
    first_failure_message  text NOT NULL DEFAULT '',
    first_trace_id         text NOT NULL DEFAULT '',
    first_run_id           uuid,
    acknowledged_at        timestamptz,
    acknowledged_by        uuid REFERENCES users (id) ON DELETE SET NULL,
    resolved_at            timestamptz,
    resolved_by            uuid REFERENCES users (id) ON DELETE SET NULL,
    -- How it was resolved: the monitor recovered, someone resolved it, or
    -- the monitor was deleted.
    resolution             text NOT NULL DEFAULT '',
    version                bigint NOT NULL DEFAULT 1,
    CONSTRAINT incidents_state CHECK (state IN ('open', 'resolved')),
    CONSTRAINT incidents_resolution CHECK ((state = 'resolved') = (resolution IN ('recovered', 'manual', 'deleted'))),
    CONSTRAINT incidents_resolved_at CHECK ((state = 'resolved') = (resolved_at IS NOT NULL))
);

-- A monitor has at most one open incident.
CREATE UNIQUE INDEX incidents_open_idx ON incidents (monitor_id) WHERE state = 'open';
-- A project's incidents, open first, newest first; IDs are UUIDv7.
CREATE INDEX incidents_project_idx ON incidents (project_id, state, id DESC);
CREATE INDEX incidents_monitor_idx ON incidents (monitor_id, id DESC);
CREATE INDEX incidents_environment_id_idx ON incidents (environment_id);
CREATE INDEX incidents_acknowledged_by_idx ON incidents (acknowledged_by);
CREATE INDEX incidents_resolved_by_idx ON incidents (resolved_by);

CREATE TABLE incident_events (
    id          uuid PRIMARY KEY,
    incident_id uuid NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL,
    -- opened, state, acknowledged, resolved, paused or resumed; alerts add
    -- notified.
    kind        text NOT NULL,
    -- Who did it; null for Signalbox.
    actor_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    from_state  text NOT NULL DEFAULT '',
    to_state    text NOT NULL DEFAULT '',
    detail      text NOT NULL DEFAULT ''
);

CREATE INDEX incident_events_incident_idx ON incident_events (incident_id, id);
CREATE INDEX incident_events_actor_id_idx ON incident_events (actor_id);

-- A monitor someone's incident was resolved by hand opens no new one until
-- it has been up once.
ALTER TABLE monitors ADD COLUMN incidents_held boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE monitors DROP COLUMN incidents_held;
DROP TABLE incident_events;
DROP TABLE incidents;
