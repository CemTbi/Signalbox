-- What people decide about a project's canvas beyond where its services go:
-- where databases and outside hosts are, once someone moves them, and the
-- dependencies people declare by hand. Owned by internal/modules/apis.
-- See docs/adr/0045-live-canvas-api.md.

-- +goose Up
CREATE TABLE canvas_targets (
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    -- The address the service graph names it by. No row needs to exist for
    -- a target to show: it comes from traffic, and this only says where.
    name       text NOT NULL,
    position_x double precision NOT NULL,
    position_y double precision NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, kind, name),
    CONSTRAINT canvas_targets_kind CHECK (kind IN ('database', 'external')),
    CONSTRAINT canvas_targets_name_length CHECK (octet_length(name) BETWEEN 1 AND 1024)
);

CREATE TABLE canvas_dependencies (
    id              uuid PRIMARY KEY,
    project_id      uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    from_service_id uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    to_service_id   uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT canvas_dependencies_pair_key UNIQUE (project_id, from_service_id, to_service_id),
    CONSTRAINT canvas_dependencies_not_itself CHECK (from_service_id <> to_service_id)
);

-- Deleting a service finds its lines through these.
CREATE INDEX canvas_dependencies_from_idx ON canvas_dependencies (from_service_id);
CREATE INDEX canvas_dependencies_to_idx ON canvas_dependencies (to_service_id);

-- +goose Down
DROP TABLE canvas_dependencies;
DROP TABLE canvas_targets;
