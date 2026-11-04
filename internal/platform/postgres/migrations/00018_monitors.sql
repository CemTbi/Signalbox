-- Monitors: checks of a project's APIs on a schedule, and each one's current
-- state. Results go to ClickHouse, so the scheduler never reads them. Owned
-- by internal/modules/monitors. See docs/adr/0046-monitors.md.

-- +goose Up
CREATE TABLE monitors (
    id                   uuid PRIMARY KEY,
    project_id           uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    environment_id       uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    -- Places the monitor's health on the canvas.
    service_id           uuid REFERENCES services (id) ON DELETE SET NULL,
    name                 text NOT NULL,
    kind                 text NOT NULL,
    request              jsonb NOT NULL,
    assertions           jsonb NOT NULL DEFAULT '[]',
    interval_seconds     integer NOT NULL,
    timeout_seconds      integer NOT NULL,
    failures_to_confirm  integer NOT NULL,
    certificate_warning_days integer NOT NULL,
    state                text NOT NULL DEFAULT 'pending',
    -- Failed checks in a row.
    consecutive_failures integer NOT NULL DEFAULT 0,
    next_check_at        timestamptz NOT NULL,
    -- When a failure's confirmation is due, outside the schedule.
    confirm_at           timestamptz,
    -- When the check in progress began. A process that stopped leaves it
    -- set, so it only counts for as long as a check can take.
    running_since        timestamptz,
    last_check_at        timestamptz,
    last_outcome         text,
    last_status          integer,
    last_duration_ms     double precision,
    last_failure_code    text,
    last_failure_message text,
    last_trace_id        text,
    last_components      jsonb,
    certificate_expires_at timestamptz,
    created_by           uuid REFERENCES users (id) ON DELETE SET NULL,
    version              bigint NOT NULL DEFAULT 1,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT monitors_kind CHECK (kind IN ('http', 'health')),
    CONSTRAINT monitors_state CHECK (state IN ('pending', 'up', 'degraded', 'down', 'paused')),
    CONSTRAINT monitors_interval CHECK (interval_seconds BETWEEN 30 AND 3600),
    CONSTRAINT monitors_timeout CHECK (timeout_seconds BETWEEN 1 AND 30),
    CONSTRAINT monitors_failures CHECK (failures_to_confirm BETWEEN 1 AND 10)
);

-- Lists a project's monitors; IDs are UUIDv7, so ID order is creation order.
CREATE INDEX monitors_project_id_idx ON monitors (project_id, id);
-- The scheduler's two questions: what's due, and what's to be confirmed.
CREATE INDEX monitors_due_idx ON monitors (next_check_at) WHERE state <> 'paused';
CREATE INDEX monitors_confirm_idx ON monitors (confirm_at) WHERE confirm_at IS NOT NULL;
-- The canvas's question: the health of an environment's services.
CREATE INDEX monitors_environment_idx ON monitors (environment_id) WHERE service_id IS NOT NULL;
CREATE INDEX monitors_service_idx ON monitors (service_id) WHERE service_id IS NOT NULL;

-- +goose Down
DROP TABLE monitors;
