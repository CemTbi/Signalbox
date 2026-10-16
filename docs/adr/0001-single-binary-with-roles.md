# 0001. One server binary with six roles

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Signalbox has six workloads with different load profiles:

| Role | Work | Grows with |
|---|---|---|
| `api` | Web app, REST and WebSocket API, every control-plane module | users and API calls |
| `mock` | Hosted mock servers | mock traffic |
| `runner` | Requests for the client, tests and monitors | requests per second |
| `scheduler` | Starting monitors and scheduled runs on time | number of monitors |
| `worker` | Lint runs, docs builds, code generation, drift analysis, notifications | queue depth |
| `ingest` | Receiving OTLP and Zipkin spans | spans per second |

Signalbox is open source and self-hosted. A team trying it on one machine must not have to run six services, while a large installation must be able to scale span ingestion without scaling the web app. The core team is small, so one codebase, one test suite and one release artifact matter.

## Decision

- Build one server binary, `signalbox-server`. The `--roles` flag (or `SIGNALBOX_ROLES`) chooses which roles a process runs: `all` on a single machine, one role per deployment on Kubernetes.
- Structure the code as a modular monolith. Each module lives in `internal/modules/<name>` and exposes a Go interface; other modules call only that interface, never another module's tables or internals. Architecture tests in CI enforce the import rules.
- Roles share no in-memory state. They coordinate through PostgreSQL and NATS ([ADR-0002](0002-data-stores.md)), so the same code works whether two roles share a process or not.
- Every process opens an ops listener (`--ops-addr`, default `:9090`) with `/livez` and `/readyz`, whatever roles it runs. Kubernetes probes use it, and it stays off the public network.
- On shutdown a process first fails readiness, waits `--drain-delay`, then stops accepting connections and lets in-flight requests finish within `--shutdown-timeout`.
- The CLI, `signalbox`, is a separate binary. It shares `pkg/specengine` with the server, so local results match the server's.
- A role that isn't implemented yet is rejected when named explicitly, and skipped by `--roles=all`.

## Consequences

- One artifact to build, sign, scan and document. A single-machine install is one container plus its databases.
- Each role scales independently on Kubernetes through its own deployment and autoscaler.
- All roles upgrade together. Database migrations must keep old and new versions working side by side (expand, then contract).
- A module can later move into its own service without a rewrite, because modules already talk through interfaces.
- The binary contains code that a given process doesn't run. That costs disk space but not memory or attack surface, because unused roles start no listeners.

## Alternatives considered

- **Microservices from day one.** Rejected. Self-hosters would run many services, and the team would spend its time on deployment plumbing instead of features.
- **A monolith without roles.** Rejected. Span ingestion and mock traffic could not scale without scaling everything.
- **Separate binaries per role from one repository.** Rejected. More artifacts to release and no benefit over a flag.
