# 0039. ClickHouse is required, and migrated like PostgreSQL

- Status: Accepted
- Date: 2026-10-30
- Deciders: project lead

## Context

[ADR-0002](0002-data-stores.md) puts spans, check results and other observed data in ClickHouse, and [ADR-0014](0014-span-storage.md) designed the span tables. Until now only the spike's tool and tests created them, with `CREATE TABLE IF NOT EXISTS` statements in Go. The server didn't read `SIGNALBOX_CLICKHOUSE_URL`.

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) makes ClickHouse required in v0.3, and its next steps write spans and check results. Before they do, the schema has to change safely across releases:
- **Upgrades:** an upgrade adds a table or a column once, even when several processes start together.
- **Retention:** spans are kept for a configured number of days.

## Decision

**ClickHouse is required.** `signalbox-server` refuses to start without `SIGNALBOX_CLICKHOUSE_URL` (or `--clickhouse-url`). At startup, once PostgreSQL is ready and migrated, it:
- waits for ClickHouse, within `SIGNALBOX_DATABASE_WAIT`, with readiness saying "waiting for ClickHouse";
- migrates it;
- applies the span retention.

The `clickhouse:responseTime` readiness check is not critical: without ClickHouse, traces can't be shown, but everything kept in PostgreSQL still works. `signalbox-server migrate` migrates both databases.

**Migrations are goose's, as PostgreSQL's are:**
- **Files:** numbered SQL files in `internal/platform/clickhouse/migrations`, embedded in the binary.
- **Version table:** applied with goose's ClickHouse dialect, which records versions in a `goose_db_version` table in ClickHouse, the same name as on PostgreSQL. goose is already a dependency, so this adds none.
- **Statements:** ClickHouse doesn't run DDL in transactions, so every file says `-- +goose NO TRANSACTION`, and its statements must be safe to run again. A file that fails halfway is applied again in full at the next start, so `CREATE … IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS` and the like are required.

**A PostgreSQL advisory lock serializes them.** ClickHouse has no lock that a session can hold, so processes take turns through `postgres.WithAdvisoryLock`, with a key of its own. Every process already has PostgreSQL, and that lock is the same mechanism goose uses there.

**Retention is configuration, not schema.**
- **What the migration does:** it creates the tables with a 7-day TTL.
- **What startup does:** it then sets `SIGNALBOX_TRACE_RETENTION_DAYS`, from 1 to 3,650 and 7 by default, on `spans`, `trace_ids` and `span_metrics_1m`, with `ALTER TABLE … MODIFY TTL`.
- **When nothing changed:** a table whose TTL already matches is left alone, so the check costs three small queries at each start.
- **When it changes:** ClickHouse recomputes when each part expires, in the background. Whole days still drop at once, as `ttl_only_drop_parts` asks.

**Each module still owns its tables.** The migration files sit together, like PostgreSQL's, and each says which module owns its tables. Only the owning module reads or writes them: `telemetry` for spans.

## Consequences

- A v0.2 installation needs ClickHouse before it upgrades. The Compose stack has run ClickHouse since v0.1, so only installations without Compose must add it. The release notes say so.
- `make dev-db` starts ClickHouse beside PostgreSQL, on `localhost:9000`, and `make run` uses it.
- Tests and `tools/spanbench` create the tables through the same migrations as the server, so they can't drift from it. `telemetry.CreateSchema` is gone.
- Retention is one setting for the whole installation. Retention per project, which RFC 0002 left for before 1.0, needs a different TTL design.

## Alternatives considered

- **Keep `CREATE TABLE IF NOT EXISTS` in Go, run at every start.** It works until the first change to an existing table, such as a new column, which then needs hand-written checks. It has no record of what ran.
- **A migration tool written for ClickHouse,** such as golang-migrate's driver, or ClickHouse's own `clickhouse-migrations`. That means a second tool and file format beside goose for one job.
- **A lock table in ClickHouse.** ClickHouse has no transactions to make taking it atomic, and two processes could both see it free. PostgreSQL's advisory locks are made for this.
- **Retention in the migration, rendered from the setting.** A migration runs once, so changing the setting later would need another mechanism anyway. Applying it at startup handles both cases.
