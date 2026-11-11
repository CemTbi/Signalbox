-- Collection and heartbeat monitors. A collection monitor runs a collection,
-- or one folder of it, and keeps each run as a test run. A heartbeat monitor
-- sends nothing: a job calls its URL, and it goes down when no call comes
-- within its interval and grace. See docs/adr/0047-collection-and-heartbeat-monitors.md.

-- +goose Up
ALTER TABLE monitors
    -- What a collection monitor runs. Deleting the collection, or the folder,
    -- deletes the monitor: there's nothing left for it to check.
    ADD COLUMN collection_id uuid REFERENCES collections (id) ON DELETE CASCADE,
    ADD COLUMN folder_id     uuid REFERENCES collection_items (id) ON DELETE CASCADE,
    -- A heartbeat's token, hashed; the token itself is shown once.
    ADD COLUMN heartbeat_token_hash bytea UNIQUE,
    -- How long past its interval a heartbeat may be late.
    ADD COLUMN grace_seconds integer NOT NULL DEFAULT 0,
    ADD COLUMN last_ping_at  timestamptz,
    -- The test run of a collection monitor's last check. Not a reference:
    -- old runs are removed, and the result outlives them.
    ADD COLUMN last_run_id   uuid,
    DROP CONSTRAINT monitors_kind,
    DROP CONSTRAINT monitors_interval,
    DROP CONSTRAINT monitors_timeout,
    ADD CONSTRAINT monitors_kind CHECK (kind IN ('http', 'health', 'collection', 'heartbeat')),
    ADD CONSTRAINT monitors_collection CHECK ((kind = 'collection') = (collection_id IS NOT NULL) AND (folder_id IS NULL OR kind = 'collection')),
    ADD CONSTRAINT monitors_heartbeat CHECK ((kind = 'heartbeat') = (heartbeat_token_hash IS NOT NULL)),
    ADD CONSTRAINT monitors_interval CHECK (CASE kind
        WHEN 'heartbeat' THEN interval_seconds BETWEEN 60 AND 2592000
        ELSE interval_seconds BETWEEN 30 AND 3600 END),
    ADD CONSTRAINT monitors_timeout CHECK (CASE kind
        WHEN 'heartbeat' THEN timeout_seconds = 0
        WHEN 'collection' THEN timeout_seconds BETWEEN 1 AND 120
        ELSE timeout_seconds BETWEEN 1 AND 30 END),
    ADD CONSTRAINT monitors_grace CHECK (grace_seconds BETWEEN 0 AND 604800 AND (grace_seconds = 0 OR kind = 'heartbeat'));

CREATE INDEX monitors_collection_id_idx ON monitors (collection_id) WHERE collection_id IS NOT NULL;
CREATE INDEX monitors_folder_id_idx ON monitors (folder_id) WHERE folder_id IS NOT NULL;

-- A collection monitor's runs. They're its, not the collection's: the
-- collection's own list leaves them out, and they're kept apart.
ALTER TABLE test_runs ADD COLUMN monitor_id uuid REFERENCES monitors (id) ON DELETE CASCADE;
CREATE INDEX test_runs_monitor_idx ON test_runs (monitor_id, id DESC) WHERE monitor_id IS NOT NULL;

-- +goose Down
DROP INDEX test_runs_monitor_idx;
ALTER TABLE test_runs DROP COLUMN monitor_id;
DELETE FROM monitors WHERE kind IN ('collection', 'heartbeat');
ALTER TABLE monitors
    DROP CONSTRAINT monitors_kind,
    DROP CONSTRAINT monitors_interval,
    DROP CONSTRAINT monitors_timeout,
    DROP COLUMN collection_id,
    DROP COLUMN folder_id,
    DROP COLUMN heartbeat_token_hash,
    DROP COLUMN grace_seconds,
    DROP COLUMN last_ping_at,
    DROP COLUMN last_run_id,
    ADD CONSTRAINT monitors_kind CHECK (kind IN ('http', 'health')),
    ADD CONSTRAINT monitors_interval CHECK (interval_seconds BETWEEN 30 AND 3600),
    ADD CONSTRAINT monitors_timeout CHECK (timeout_seconds BETWEEN 1 AND 30);
