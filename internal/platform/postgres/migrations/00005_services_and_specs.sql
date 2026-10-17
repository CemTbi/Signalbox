-- Services on a project's canvas, their OpenAPI specs and every saved
-- revision of each spec. Owned by internal/modules/apis.
-- See docs/adr/0019-spec-revisions.md.

-- +goose Up
CREATE TABLE services (
    id                     uuid PRIMARY KEY,
    project_id             uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                   text NOT NULL,
    slug                   text NOT NULL,
    description            text NOT NULL DEFAULT '',
    -- The service.name in the service's traces; links spans to the node.
    telemetry_service_name text,
    -- The node's place on the canvas, in canvas units.
    position_x             double precision NOT NULL DEFAULT 0,
    position_y             double precision NOT NULL DEFAULT 0,
    version                bigint NOT NULL DEFAULT 1,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT services_project_slug_key UNIQUE (project_id, slug),
    CONSTRAINT services_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 63),
    CONSTRAINT services_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT services_description_length CHECK (char_length(description) <= 1000),
    CONSTRAINT services_telemetry_name_length CHECK (char_length(telemetry_service_name) BETWEEN 1 AND 255),
    CONSTRAINT services_position_finite CHECK (position_x BETWEEN -1e9 AND 1e9 AND position_y BETWEEN -1e9 AND 1e9)
);

-- Lists a project's services in ID order.
CREATE INDEX services_project_id_idx ON services (project_id, id);

CREATE TABLE specs (
    id                  uuid PRIMARY KEY,
    service_id          uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    name                text NOT NULL,
    -- Set in the same transaction that creates the spec's first revision.
    current_revision_id uuid,
    -- The number the next revision gets.
    next_revision       integer NOT NULL DEFAULT 1,
    -- Increments on every change, including a new revision.
    version             bigint NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT specs_name_length CHECK (char_length(name) BETWEEN 1 AND 100)
);

-- Lists a service's specs in ID order.
CREATE INDEX specs_service_id_idx ON specs (service_id, id);

-- A saved version of a spec's document. Rows never change once written.
CREATE TABLE spec_revisions (
    id              uuid PRIMARY KEY,
    spec_id         uuid NOT NULL REFERENCES specs (id) ON DELETE CASCADE,
    number          integer NOT NULL,
    message         text NOT NULL DEFAULT '',
    content         text NOT NULL,
    -- yaml or json, as the document was written.
    file_type       text NOT NULL,
    -- From the spec engine: openapi-3.1, and the version the document
    -- declares, such as 3.1.1.
    format          text NOT NULL,
    openapi_version text NOT NULL,
    -- info.title and info.version of the document.
    title           text NOT NULL DEFAULT '',
    api_version     text NOT NULL DEFAULT '',
    size_bytes      integer NOT NULL,
    sha256          bytea NOT NULL,
    errors          integer NOT NULL,
    warnings        integer NOT NULL,
    infos           integer NOT NULL,
    hints           integer NOT NULL,
    -- The full check report, diagnostics included.
    report          jsonb NOT NULL,
    -- Who saved it. Accounts that saved revisions can't be deleted, so the
    -- history keeps its authors.
    created_by      uuid NOT NULL REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT spec_revisions_number_key UNIQUE (spec_id, number),
    CONSTRAINT spec_revisions_number_positive CHECK (number >= 1),
    CONSTRAINT spec_revisions_file_type CHECK (file_type IN ('yaml', 'json')),
    CONSTRAINT spec_revisions_message_length CHECK (char_length(message) <= 500),
    CONSTRAINT spec_revisions_size CHECK (size_bytes BETWEEN 1 AND 10485760),
    CONSTRAINT spec_revisions_sha256_length CHECK (octet_length(sha256) = 32),
    CONSTRAINT spec_revisions_counts CHECK (errors >= 0 AND warnings >= 0 AND infos >= 0 AND hints >= 0)
);

-- The revision a spec shows. Deferred, so a spec and its first revision can
-- be created in either order within one transaction.
ALTER TABLE specs ADD CONSTRAINT specs_current_revision_fkey
    FOREIGN KEY (current_revision_id) REFERENCES spec_revisions (id) DEFERRABLE INITIALLY DEFERRED;

-- spec_revisions_number_key lists a spec's history. This index keeps the
-- foreign key check fast if an account is ever deleted.
CREATE INDEX spec_revisions_created_by_idx ON spec_revisions (created_by);

-- +goose Down
ALTER TABLE specs DROP CONSTRAINT specs_current_revision_fkey;
DROP TABLE spec_revisions;
DROP TABLE specs;
DROP TABLE services;
