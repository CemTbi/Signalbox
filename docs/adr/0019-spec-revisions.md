# 0019. Services, specs and their revisions

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

v0.1 is done when a frontend developer builds against a mock without help from the backend team. Mocks, docs, code generation and the canvas all start from the same thing: a service's OpenAPI document, stored by Signalbox, checked, and kept in versions people can point at. `api/openapi.yaml` has described services, specs and revisions since its first draft; this decision is how they are stored.

## Decision

**Services** belong to a project and appear as nodes on its canvas. Each has:
- a name;
- a slug, unique within the project, which will name its mock;
- an optional description;
- the `service.name` its traces use;
- its position on the canvas.

**A service has specs**, usually one. A spec is an OpenAPI or Swagger document with a name. Its content lives in revisions.

**Every save is a revision, and revisions never change.**
- **What a revision keeps:** the document exactly as it was sent; its file type; its SHA-256; the format and OpenAPI version it declares; its `info.title` and `info.version`; the author and an optional message; and the spec engine's full report.
- **Checks:** the document is checked with the recommended rules before the save's transaction starts, so a slow check holds no locks.
- **Documents with problems:** these are saved; the report records the problems, and publishing will be where problems block. A document that can't be read as OpenAPI or Swagger at all is refused with `unreadable-document`, because nothing downstream can use it.
- **Numbering:** revisions are numbered from 1 within a spec.

**Saves can't overwrite each other.** Saving needs `If-Match` with the spec's current ETag (428 without, 412 when stale). The ETag changes with every save and rename.

**Documents are stored in PostgreSQL**, up to 10 MiB each, as text that PostgreSQL compresses.

**Permissions follow projects:**
- every workspace member reads;
- owners, admins and editors add, change and save;
- owners and admins delete.

The `apis` module asks the workspaces module for the user's role in the project, rather than reading its tables ([ADR-0001](0001-single-binary-with-roles.md)).

**Published versions** (a revision with a name such as `1.4.0`, which mocks, docs and code generation serve) come in the next change, with the mocks that need them.

## Consequences

- Any revision can be fetched, compared or restored forever, and its content can be cached forever.
- Every revision is stored in full. A 1 MB spec saved 500 times takes about 500 MB before PostgreSQL's compression, which typically cuts YAML to a quarter or less. If that becomes a problem, revisions can be deduplicated by hash or stored as deltas without changing the API.
- Accounts that saved revisions can't be deleted, so the history keeps its authors. Account deletion, when it comes, must reassign or anonymize authors.
- The role check isn't in the same transaction as the write. Someone removed from a workspace at that instant may complete one change.
- `Idempotency-Key` is accepted on creates but not acted on yet, as the contract says.

## Alternatives considered

- **Keep only the latest document, with a change log.** Rejected. Mocks and docs serve published versions while people keep editing, and diffs need both sides.
- **Store documents in object storage.** Rejected for now. Specs are state that people edit, and at their size PostgreSQL keeps backups and transactions simple. Large response bodies and generated code will go to object storage, as [ADR-0002](0002-data-stores.md) plans.
- **Refuse documents with errors.** Rejected. Specs get written over many saves, and the editor would lose work. Errors block publishing instead.
- **Git as the store.** Deferred. Two-way Git sync is planned after 1.0; revisions map onto commits when it comes.
