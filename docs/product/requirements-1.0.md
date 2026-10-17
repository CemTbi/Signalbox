# Signalbox 1.0 requirements

This document defines what Signalbox 1.0 is. The "In 1.0" lists are commitments. New work joins them only through an [RFC](../rfcs/README.md), and something of the same size leaves.

## Vision

Signalbox is an open-source, self-hosted platform for the whole life of an HTTP API. Every module works from the same OpenAPI document, so a change to the spec reaches mocks, docs, saved requests, tests and monitors at once, and production traces are checked against it.

## Users

The people who work on APIs inside one organization: backend and frontend developers, QA engineers, platform and SRE teams, and whoever owns the API contract. One installation serves many teams through workspaces and projects.

## Principles

1. **One source of truth.** The OpenAPI document drives every module.
2. **Standards over custom formats.** OpenAPI, Arazzo, OTLP, Zipkin, W3C Trace Context, RFC 9457, RFC 9535 JSONPath, Standard Webhooks.
3. **Self-hosting is the product.** Install in under 10 minutes, upgrade without downtime, run air-gapped.
4. **Secure by default.** Single sign-on for everyone, encrypted secrets, a runner that can't reach places it shouldn't.
5. **API-first.** The web app uses only the public API, which is described in `api/openapi.yaml`.

## Constraints

Decided by the maintainer on 16 October 2026:

| Constraint | Decision | What follows |
|---|---|---|
| License | Apache-2.0 ([ADR-0007](../adr/0007-license-and-dco.md)) | Companies can adopt Signalbox without legal review friction. |
| Team | One maintainer, with Claude as the AI assistant that writes most of the code | The maintainer reviews and merges every pull request. The roadmap is a sequence of milestones without calendar dates; its pace is set by review time. |
| Protocols | HTTP APIs described with OpenAPI first | GraphQL, gRPC, WebSockets, server-sent events and event-driven APIs (AsyncAPI) follow after 1.0. Specs and revisions record their format, so other formats can join without a new data model. |
| Budget | Under €100, free services first | CI runs on GitHub Actions, images go to GitHub Container Registry, releases are signed with Sigstore, and docs are served by GitHub Pages, all free for public repositories. Binaries aren't signed with paid Apple or Windows certificates; checksums and Sigstore signatures prove where they came from. |

## Modules

### Workspace canvas

The home screen of every project: each service is a node with its live state, and lines show who calls whom.

- **In 1.0:** service nodes with spec version, health, p95 latency and error rate; lines from traffic seen in traces plus dependencies declared by hand; databases and external hosts added from trace data; an environment switcher; a side panel per service with every module one click away; automatic layout, saved manual positions, groups by team or domain.
- **After 1.0:** replaying one trace across the canvas; C4 and Mermaid export; live cursors.

### API designer

Write and evolve OpenAPI documents in a code editor or with forms; both stay in sync.

- **In 1.0:** code editor with completion, hover docs, go to `$ref` and rename; a form editor whose edits patch the YAML in place and keep comments and key order; multi-file specs; a revision on every save, a named version on every publish, a diff between any two; import from a file, a URL, a Postman collection, HAR or cURL. Supports OpenAPI 3.0, 3.1 and 3.2, imports Swagger 2.0, applies Overlay 1.0.
- **After 1.0:** branches and change requests with review; two-way Git sync; a shared schema library; real-time co-editing.

### Validation and governance

Catch broken, inconsistent or risky specs before anyone writes code against them.

- **In 1.0:** syntax, structure and `$ref` errors with line and column; built-in style rules, custom rules per workspace, OWASP API Security rules; a quality score per spec; breaking-change reports between versions; a publish gate; identical checks in the editor, the CLI and CI.
- **After 1.0:** pull request comments through a GitHub App; rule packs per team.

### Mock servers

A working fake of any API version seconds after it is written.

- **In 1.0:** one URL per mock, path-based by default; responses from examples or generated from schemas, deterministic per request; request validation with RFC 9457 errors that name the field; status and example chosen with the `Prefer` header; conditional rules; latency and error injection; a request log; reload on spec change; local mocks from the CLI.
- **After 1.0:** a stateful mode; record and replay from real traffic.

### Documentation

Reference docs that are always current, for every published version.

- **In 1.0:** generated on publish; grouped by tags (including 3.2 nested tags); code samples in curl, JavaScript, Python, Go, Java and C#; a Try it console that sends requests through the runner, against the mock, staging or production; a version switcher; internal or public visibility; a stock Swagger UI view; static HTML export.
- **After 1.0:** guides next to the reference; custom domains and theming.

### Code generation

Server stubs and client SDKs from any published version.

- **In 1.0:** server stubs for Go, Java Spring, Python FastAPI and ASP.NET Core; client SDKs for TypeScript, Python, Go, Java and C#; output pinned to a spec version; download as a zip or run `signalbox codegen`; a warning when a 3.1 or 3.2 feature can't be expressed for the chosen generator.
- **After 1.0:** generated SDKs opened as pull requests; custom templates.

### API client

Send real requests, read real responses, keep them organized.

- **In 1.0:** collections generated from a spec or built by hand; environments and encrypted secret variables; API key, Basic, Bearer, OAuth 2.0 (authorization code with PKCE, client credentials, device code) and client certificates; pre-request and test scripts with a `pm`-compatible API; a timing breakdown; every response checked against the spec; history, code snippets and Open trace; runs on the server or through `signalbox agent` on a laptop. Imports Postman Collection v2.1, Insomnia, HAR and cURL.
- **After 1.0:** GraphQL, WebSocket, server-sent events, gRPC.

### Testing

Repeatable checks that run in CI.

- **In 1.0:** multi-step suites with variables passed between steps; assertions on status, headers, body values (RFC 9535 JSONPath), schema and response time; data-driven runs from CSV or JSON; contract tests generated per operation; runs from the UI, on a schedule, or `signalbox test run` in CI with JUnit, JSON and HTML reports; GitHub Actions and GitLab CI templates; Arazzo 1.0 import and export; every step linked to its trace.
- **After 1.0:** property-based fuzzing; load tests; flaky-test detection.

### Health monitoring

Know within a minute when an API stops working, and why.

- **In 1.0:** HTTP checks every 30 seconds to 1 hour; multi-step checks reusing test suites; TLS certificate expiry, DNS and TCP checks; heartbeats for cron jobs; reads `application/health+json`, Kubernetes `/livez` and `/readyz` and Spring Boot Actuator responses and shows component status on the canvas; failures confirmed from a second runner location; incidents with acknowledge and resolve; alerts by email, Slack, Microsoft Teams, PagerDuty and webhooks; maintenance windows; an internal status page.
- **After 1.0:** SLOs with error budgets; public status pages with subscribers.

### Distributed tracing

Follow one request through every service it touches.

- **In 1.0:** OTLP over gRPC (`:4317`) and HTTP (`:4318`), Zipkin v2 (`POST /api/v2/spans` on `:9411`), W3C Trace Context and B3; search by service, operation, status, duration and attributes; a waterfall with span details and the critical path; a service dependency graph that also draws the canvas lines; rate, errors and latency per service and operation; each HTTP span linked to its OpenAPI operation; a drift report (endpoints in traffic but not in the spec, documented endpoints nobody calls, undocumented status codes); secrets redacted at ingest; retention per project, 7 days by default.
- **After 1.0:** logs and metrics next to traces; schema drift on bodies; a Zipkin-compatible query API.

### Platform services

- **In 1.0:** local accounts with passkeys or TOTP; OIDC and SAML single sign-on for everyone; workspaces, projects, roles, service accounts and scoped API tokens; an audit log; a public REST API covering everything the UI does; signed webhooks; the `signalbox` CLI and agent for Linux, macOS and Windows.
- **After 1.0:** LDAP and SCIM; a Terraform provider; plugins.

## Targets

| Measure | Single machine (4 vCPU, 16 GB RAM, SSD) | Kubernetes |
|---|---|---|
| Active users | 50 | 1,000+ |
| Spans ingested, sustained | 5,000 per second | 100,000 per second |
| Monitors at a 1-minute interval | 1,000 | 50,000 |
| Latency a mock adds | p99 < 10 ms | p99 < 10 ms |
| REST API reads and writes | p95 < 200 ms and < 500 ms | same |
| Lint results for a 10,000-line spec | < 1 s | < 1 s |
| Trace by ID, search over 24 hours | p95 < 1 s and < 3 s | same |
| Monitor start jitter | p99 < 2 s | p99 < 2 s |
| Web app first load | LCP < 2.5 s, at most 350 kB of JavaScript before the editor loads | same |
| Losing one node | not applicable | no downtime, no lost acknowledged writes |

Backups: PostgreSQL point-in-time recovery with at most 5 minutes of data loss; restore within 1 hour.

## Roadmap

Milestones come in this order. Each one is a series of pull requests and is done when its last column holds.

| Milestone | Contents | Done when |
|---|---|---|
| Walking skeleton (internal) | `docker compose up` gives sign-in with local accounts or OIDC, inviting people to a workspace, a project and an empty canvas; CI builds signed images; the first ADRs are merged | All of the contents work from a clean machine |
| v0.1 alpha | Design and validate specs, publish docs, hosted mocks, code generation, lint and diff in CI, services placed on the canvas by hand | A frontend developer builds against a mock without help from the backend team |
| v0.2 | API client with the local agent, test suites, generated contract tests, CI templates | Real Postman collections import with a clean report, and a test suite gates a pull request |
| v0.3 beta | Monitors, incidents and alerts, tracing over OTLP and Zipkin, service graph, live canvas | The demo shop shows live traffic on the canvas, and a failing health check reaches Slack within two minutes |
| 1.0 | Drift report, SAML, audit log, high-availability Helm chart, performance targets met, security review closed, documentation complete | Every target above passes, no high-severity finding is open, and the upgrade from 0.3 is tested |

The security review before 1.0 is an external audit if a funding program for open-source security, such as OSTIF or Alpha-Omega, accepts the project. Otherwise it is a published threat model, fuzzing of every parser, and a fix for every high-severity finding from the automated scanners.

### Progress

| Item | State |
|---|---|
| Spec engine (spike 1): parse, validate and lint 2.0 to 3.2 with positions, 10,000 lines in 0.19 s | Done |
| `signalbox lint` | Done |
| `signalbox-server` api role, health endpoints, spec checks over HTTP | Done |
| ADR-0001 to ADR-0016 | Done |
| `api/openapi.yaml` v0 | Done |
| Spike 2: ClickHouse span schema at 5,000 spans per second (ADR-0014) | Done |
| Spike 5: the runner's safe dialer (ADR-0016) | Done |
| Spikes 3 and 4: canvas with 200 live nodes, Sobek with Postman scripts | Not started |
| Sign-in with local accounts, workspaces and projects backed by PostgreSQL | Done |
| App shell, sign-in and setup pages, workspace and project pages, empty canvas | Done |
| End-to-end journey in CI against the Compose stack, with accessibility checks | Done |
| Name, license, team, protocols and budget decided | Done |
| Inviting people to a workspace, roles of members | Done |
| Sign-in with OIDC | Done |
| Row-level security as a second line of access control (ADR-0009) | Not started |
| Design system | Started |

## After 1.0

- **More protocols:** GraphQL, gRPC, WebSockets and server-sent events in the client, tests and mocks, and AsyncAPI documents for event-driven APIs such as Kafka and MQTT.

## Out of scope for 1.0

- Hosting or deploying users' own APIs
- A low-code builder that runs endpoint logic
- Protocols other than HTTP in the client, tests and mocks (planned after 1.0, see above)
- A hosted, multi-tenant Signalbox service
