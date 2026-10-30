-- Ingest keys, which let services send spans to one project environment.
-- Owned by internal/modules/telemetry.
-- See docs/rfcs/0002-monitors-and-tracing.md and docs/adr/0040-span-ingest.md.

-- +goose Up
CREATE TABLE ingest_keys (
    id             uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    name           text NOT NULL,
    -- SHA-256 of the whole key, sbx_ing_ included; the key isn't stored.
    key_hash       bytea NOT NULL,
    -- The key's last four characters, to tell keys apart.
    hint           text NOT NULL,
    created_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_used_at   timestamptz,
    CONSTRAINT ingest_keys_key_hash_key UNIQUE (key_hash),
    CONSTRAINT ingest_keys_key_hash_length CHECK (octet_length(key_hash) = 32),
    CONSTRAINT ingest_keys_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT ingest_keys_hint_length CHECK (char_length(hint) = 4)
);

-- Lists an environment's keys; IDs are UUIDv7, so ID order is creation order.
CREATE INDEX ingest_keys_environment_id_idx ON ingest_keys (environment_id, id);

-- +goose Down
DROP TABLE ingest_keys;
