# RFCs

An RFC (request for comments) proposes a significant change and keeps the reasoning after the discussion ends.

## When you need one

- a new module, role or user-facing feature area
- a change to the public REST API, the data model or the security model
- a new runtime dependency in a shipped artifact that brings in a large dependency tree
- a change to [GOVERNANCE.md](../../GOVERNANCE.md)

Bug fixes, small features inside an existing module and refactors don't need an RFC.

## Process

1. Copy [0000-template.md](0000-template.md) to `NNNN-short-title.md`, using the next free number, and open a pull request labelled `rfc`.
2. Discussion happens on the pull request. Update the document as the proposal changes, so it always shows the current state.
3. When discussion settles, a maintainer announces a final comment period of seven days.
4. The RFC is accepted when at least two maintainers approve and no maintainer objects. It merges with status **Accepted**. A rejected RFC also merges, with status **Rejected**, so the reasoning isn't lost.
5. Implementation happens in separate pull requests that link to the RFC.

Decisions about architecture that come out of an RFC are also recorded as [ADRs](../adr/README.md).

## Index

| RFC | Title | Status |
|---|---|---|
| [0001](0001-api-client-and-tests.md) | The API client, tests and the local agent (v0.2) | Accepted |
| [0002](0002-monitors-and-tracing.md) | Monitors, alerts, tracing and the live canvas (v0.3 beta) | Accepted |
