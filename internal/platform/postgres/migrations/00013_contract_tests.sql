-- Contract tests: a collection generated from a published spec version,
-- with a request per operation. Generating again for a newer version
-- replaces the generated items and keeps the rest. Owned by
-- internal/modules/client. See docs/adr/0035-contract-tests.md.

-- +goose Up
ALTER TABLE collections
    -- The spec the collection's contract tests come from, and the version
    -- they were last generated from.
    ADD COLUMN contract_spec_id uuid REFERENCES specs (id) ON DELETE SET NULL,
    ADD COLUMN contract_version_id uuid REFERENCES spec_versions (id) ON DELETE SET NULL;

-- A spec's contract tests are one collection per project.
CREATE UNIQUE INDEX collections_project_id_contract_spec_id_key ON collections (project_id, contract_spec_id) WHERE contract_spec_id IS NOT NULL;
CREATE INDEX collections_contract_spec_id_idx ON collections (contract_spec_id);
CREATE INDEX collections_contract_version_id_idx ON collections (contract_version_id);

ALTER TABLE collection_items
    -- Generated items are replaced when the contract tests are generated
    -- again; items people add are kept.
    ADD COLUMN generated boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE collection_items DROP COLUMN generated;
DROP INDEX collections_contract_version_id_idx;
DROP INDEX collections_contract_spec_id_idx;
DROP INDEX collections_project_id_contract_spec_id_key;
ALTER TABLE collections DROP COLUMN contract_version_id, DROP COLUMN contract_spec_id;
