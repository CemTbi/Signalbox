# 0020. Published versions and the publish gate

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

[ADR-0019](0019-spec-revisions.md) keeps every save of a spec as a revision. Revisions are drafts: people save half-finished work. Mocks, docs and generated code need something that holds still and is fit to share, and a way to tell the two apart. The build plan asks for a publish gate: errors, and later unapproved breaking changes, block a release.

## Decision

**A version is a published revision under a name**, such as `1.4.0`. A version holds:
- its name;
- the revision it publishes;
- optional release notes, up to 2,000 characters;
- who published it, and when.

**Rules:**
- **Versions never change.** A name is used once per spec.
- **Names:** letters, digits, dots, hyphens, underscores and plus signs, starting with a letter or digit, up to 64 characters. That covers semantic versions with pre-release and build parts, and dates such as `2026-10`.
- **Defaults:** publishing takes the spec's current revision, named after the document's own `info.version`. Any other revision of the spec can be published by ID.
- **The gate:** a revision with errors can't be published, and the API answers `422 spec-has-errors` with the count. Warnings don't block. The web app shows why instead of offering a button that would fail.
- **Who may publish:** owners, admins and editors, the same people who save revisions.

**Ordering:** versions are listed newest first, by their UUIDv7 IDs, which sort by time.

**Mocks, docs and code generation will serve versions:** the newest by default, or a named one. They never serve a draft.

## Consequences

- People can keep editing while what others use stays put.
- A published mistake can't be deleted or renamed; it is replaced by publishing a fixed revision under a new name. Unpublishing, if needed, will be an admin action that keeps the record.
- The breaking-change check joins the gate when spec diffs land: a breaking change will need an explicit approval to publish.

## Alternatives considered

- **Tags that can move**, like Git branches. Rejected. A mock or SDK pinned to `1.4.0` must keep meaning the same thing.
- **Publishing on every save without errors.** Rejected. Saves are drafts, and a half-finished spec without errors is still half-finished.
- **Blocking on warnings too.** Rejected for now. Many real specs carry style warnings for a long time; rule sets per team, planned for 1.0, can promote rules to errors.
