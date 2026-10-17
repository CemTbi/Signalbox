-- Published versions of specs: a revision with a name, which mocks, docs
-- and code generation serve. Owned by internal/modules/apis.
-- See docs/adr/0020-published-versions.md.

-- +goose Up
CREATE TABLE spec_versions (
    id           uuid PRIMARY KEY,
    spec_id      uuid NOT NULL REFERENCES specs (id) ON DELETE CASCADE,
    name         text NOT NULL,
    revision_id  uuid NOT NULL REFERENCES spec_revisions (id) ON DELETE CASCADE,
    notes        text NOT NULL DEFAULT '',
    published_by uuid NOT NULL REFERENCES users (id),
    published_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT spec_versions_spec_name_key UNIQUE (spec_id, name),
    CONSTRAINT spec_versions_name_format CHECK (name ~ '^[0-9A-Za-z][0-9A-Za-z.+_-]*$' AND char_length(name) <= 64),
    CONSTRAINT spec_versions_notes_length CHECK (char_length(notes) <= 2000)
);

-- Lists a spec's versions; IDs are UUIDv7, so ID order is publishing order.
CREATE INDEX spec_versions_spec_id_idx ON spec_versions (spec_id, id);
-- Keeps the foreign key checks fast.
CREATE INDEX spec_versions_revision_id_idx ON spec_versions (revision_id);
CREATE INDEX spec_versions_published_by_idx ON spec_versions (published_by);

-- +goose Down
DROP TABLE spec_versions;
