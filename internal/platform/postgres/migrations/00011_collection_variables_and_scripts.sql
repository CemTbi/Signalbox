-- Collection variables, and the pre-request and test scripts of
-- collections, folders and requests, as Postman collections carry them.
-- Owned by internal/modules/client. See
-- docs/rfcs/0001-api-client-and-tests.md and docs/adr/0031-postman-import.md.

-- +goose Up
ALTER TABLE collections
    -- [{key, value}], in order. Environment variables win over them.
    ADD COLUMN variables jsonb NOT NULL DEFAULT '[]',
    -- {prerequest, test}: JavaScript, run with the requests in it.
    ADD COLUMN scripts jsonb NOT NULL DEFAULT '{}',
    ADD CONSTRAINT collections_variables_size CHECK (pg_column_size(variables) <= 4194304),
    ADD CONSTRAINT collections_scripts_size CHECK (pg_column_size(scripts) <= 1048576);

ALTER TABLE collection_items
    ADD COLUMN scripts jsonb NOT NULL DEFAULT '{}',
    ADD CONSTRAINT collection_items_scripts_size CHECK (pg_column_size(scripts) <= 1048576);

-- +goose Down
ALTER TABLE collection_items DROP COLUMN scripts;
ALTER TABLE collections DROP COLUMN scripts, DROP COLUMN variables;
