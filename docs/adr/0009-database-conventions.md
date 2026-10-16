# 0009. Database conventions

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

The walking skeleton adds Signalbox's first PostgreSQL tables: accounts, sessions, workspaces, members, projects and environments. Every module will add more. Without shared conventions, each module would invent its own IDs, timestamps, concurrency checks and migration habits, and rolling upgrades would break the first time a migration removed something the previous release still used.

## Decision

**IDs.** Rows are keyed by UUIDv7, stored in PostgreSQL's `uuid` type. UUIDv7 sorts by creation time, so primary-key indexes stay compact and lists page in creation order. The API shows IDs as a type prefix and the UUID in 26 lowercase Crockford base32 characters, such as `ws_01h455vb4pex5vsknk084sn02q`, following the TypeID specification. `internal/platform/id` creates and converts them. IDs are generated in Go, not by the database, because PostgreSQL 16 has no `uuidv7()`.

**Columns.** `created_at` and `updated_at` are `timestamptz` and default to `now()`. Resources people edit have `version bigint`, incremented on every change, which the API sends as the `ETag`. Database CHECK constraints repeat the API's validation (lengths, slug format, enumerations), so bad data can't enter through any path.

**Names.** Tables are plural snake_case. Unique constraints are named `<table>_<column>_key` and indexes `<table>_<columns>_idx`. The code maps constraint names to API errors, such as `workspaces_slug_key` to `409 slug-taken`, so the names are part of the contract between migrations and code.

**Ownership.** Each table belongs to one module, and its migration says which. Other modules never query it; they call the owning module. Foreign keys to `users(id)` are the one exception, because every module records who did what.

**Migrations.** Plain SQL files for goose, numbered `00001_name.sql`, embedded in the binary from `internal/platform/postgres/migrations`. The server applies pending migrations at startup under a PostgreSQL advisory lock, so replicas that start together take turns; `--migrate=false` and `signalbox-server migrate` let operators run them as a separate step. Migrations follow expand, then contract: a release never drops, renames or tightens anything the previous release still uses. Down migrations exist for development only.

**Tests.** Tests run against real PostgreSQL through Testcontainers, never a fake. Each test gets its own copy of a migrated template database, which takes milliseconds. CI fails if the database tests skip.

## Consequences

- IDs reveal roughly when a row was created. That is acceptable for the resources Signalbox stores, and it is why secrets and tokens are never UUIDs.
- A failed constraint is caught twice, in Go with a helpful message and in the database as a last line of defense.
- Row-level security, the second line of defense in [ADR-0004](0004-authentication-and-sessions.md), isn't enabled yet. It needs the server to connect as a role that isn't the tables' owner or a superuser, and the default Compose setup connects as a superuser. It is planned before v0.1, together with a separate migration role.

## Alternatives considered

- **Serial integer IDs.** Rejected. They leak counts, collide when data moves between installations, and need a database round trip to know a new row's ID.
- **ULIDs stored as text.** Rejected. Twice the storage of `uuid` and slower comparisons, for the same ordering.
- **Generated query code with sqlc.** Deferred. Its PostgreSQL parser needs cgo, which complicates Windows development. The modules use pgx directly until the query count makes generation worth it.
