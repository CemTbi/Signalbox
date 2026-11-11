-- A collection monitor's check is a test run: its result names the run.
-- See docs/adr/0047-collection-and-heartbeat-monitors.md.

-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE check_results ADD COLUMN IF NOT EXISTS run_id Nullable(UUID);

-- +goose Down
ALTER TABLE check_results DROP COLUMN IF EXISTS run_id;
