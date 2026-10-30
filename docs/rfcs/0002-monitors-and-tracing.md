# 0002. Monitors, alerts, tracing and the live canvas (v0.3 beta)

- Status: In discussion
- Authors: project lead, Claude
- Discussion: the pull request that adds this file

## Summary

v0.3 makes Signalbox watch APIs in production, as well as design and test them:
- **monitors:** HTTP checks on a schedule, which read health formats and certificate expiry, collection runs as multi-step checks, and heartbeats for cron jobs;
- **incidents and alerts:** a confirmed failure opens an incident, which people acknowledge and resolve, and alerts reach Slack, Microsoft Teams, PagerDuty and signed webhooks;
- **tracing:** spans arrive over OTLP (gRPC and HTTP) and Zipkin v2, are stored in ClickHouse ([ADR-0014](../adr/0014-span-storage.md)), and can be searched and opened as a waterfall;
- **a service graph** drawn from the spans;
- **a live canvas:** each service shows its health, rate, error rate and p95 latency, and lines show who calls whom;
- **a demo shop:** a few small instrumented services that send real traffic, for trying Signalbox and for its own tests.

The milestone is done when the demo shop shows live traffic on the canvas, and a failing health check reaches Slack within two minutes.

## Motivation

After v0.2, a team designs an API in Signalbox, mocks it, tests it in CI and calls it from the client. Once the API is deployed, they leave for other tools: an uptime checker to know it's up, Zipkin or Jaeger to see why it's slow, and a diagram someone draws by hand to know what calls what. None of those know the spec, and the diagram is out of date the week it's drawn.

v0.3 brings those jobs in. The canvas, which today shows services placed by hand, becomes the live picture of a project, from the same data the monitors and traces keep.

## Proposal

### Modules and roles

| Module | Owns |
|---|---|
| `internal/modules/monitors` | Monitors, their schedule and state, check results, heartbeats, and incidents. |
| `internal/modules/alerts` | Alert channels and delivering notifications. |
| `internal/modules/telemetry` (extended) | Ingest keys, receiving spans, trace search, the service graph and per-service metrics. |

Three roles from [ADR-0001](../adr/0001-single-binary-with-roles.md) become available:
- **`scheduler`** starts checks on time and hands them to the runner.
- **`worker`** delivers notifications and builds the service graph.
- **`ingest`** receives spans.

`--roles=all` runs all of them. Until NATS arrives with the high-availability work, `scheduler` and `worker` run in the same process as `api` and `runner`, as the runner does today ([ADR-0027](../adr/0027-runner-and-history.md)). They already coordinate through PostgreSQL, so several such processes can run side by side. `ingest` needs only PostgreSQL, to check keys, and ClickHouse, so it can run on its own and scale with span volume from the start.

### ClickHouse in production

v0.3 is the first code to write to ClickHouse outside the spike. ClickHouse becomes required, as PostgreSQL did in v0.1. The Compose stack already runs it.
- **Settings:** the server reads `SIGNALBOX_CLICKHOUSE_URL`, waits for ClickHouse at startup, and reports it in `/readyz`.
- **Migrations:** versioned, like PostgreSQL's, in a `schema_migrations` table in ClickHouse. ClickHouse has no advisory locks, so one PostgreSQL advisory lock serializes them across processes.
- **Tables:** the spike's three (`spans`, `trace_ids`, `span_metrics_1m`), plus `check_results` and `service_edges_1m`, below.
- **Retention:** one setting per installation, `SIGNALBOX_TRACE_RETENTION_DAYS`, 7 by default. Retention per project comes before 1.0.

### Monitors

**Kinds:**
- **HTTP:** a request in the client's request model: method, URL, headers, body and auth, with the environment's variables and secrets filled in. The check passes when it gets a response and every assertion holds. The default assertion is a 2xx status, and others check the status, a header, a value at an RFC 9535 JSONPath, and the response time.
- **Health:** an HTTP check that also reads the body, if it's one of these:
  - `application/health+json`;
  - Spring Boot Actuator's `/actuator/health`;
  - Kubernetes `/readyz?verbose`.

  `warn`, or a component that's down while the whole is up, makes the monitor **degraded** rather than down. The components are kept with each result and shown on the canvas.
- **TLS:** every HTTPS check records when the server's certificate expires, and a monitor can turn degraded when expiry is closer than a number of days, 14 by default.
- **Collection:** runs a collection or folder with the v0.2 engine ([ADR-0033](../adr/0033-collection-runs.md)). It passes when the run passes, and every step's results are kept as for any run.
- **Heartbeat:** has no request. A cron job calls `POST /api/v1/heartbeats/{token}` when it finishes, and the monitor goes down when no call comes within the interval plus a grace period.

DNS and TCP checks come before 1.0.

**Each monitor has** a project environment, optionally a service (which places its health on the canvas), an interval from 30 seconds to 1 hour, a timeout, a number of failures to confirm (2 by default), its alert channels, and whether it's paused.

**State:** `pending` until the first check, then `up`, `degraded` or `down`, or `paused`. A failure doesn't change the state on its own: it starts a confirmation, a second check 10 seconds later. Only the number of failures in a row set by the monitor makes it `down`. One success brings it back `up`.

**Scheduling:**
- Each monitor's row holds `next_check_at`. The scheduler wakes every second, claims due monitors with `FOR UPDATE SKIP LOCKED`, and moves their `next_check_at` on by one interval before handing them to the runner. Several schedulers never start the same check.
- Monitors with the same interval are spread across it by their ID, so a thousand one-minute monitors don't all start at :00.
- A check that's still running when the next one is due is skipped, and the result says so.
- Target: a check starts within 2 seconds of its time at p99, for 1,000 monitors at a 1-minute interval on one machine.

**Where checks run:** on the server's runner, through the egress policy ([ADR-0016](../adr/0016-runner-egress.md)). Monitors are the project's, not one person's, so they don't run on agents in v0.3. Checks from a second runner location, to confirm a failure, come with NATS before 1.0.

**Check results** go to ClickHouse, in `check_results`, partitioned by day and kept for 90 days. Each holds:
- the monitor, the environment and the time;
- the outcome and the duration;
- the status code, the failure code and its message;
- the health components and the certificate's expiry;
- the trace ID the check sent.

PostgreSQL keeps only each monitor's current state, so the scheduler never reads ClickHouse. Uptime over a period, and the response time chart, are read from `check_results`.

### Incidents

- **Opening:** a monitor going `down` opens an incident, and a monitor has at most one open incident. Going `degraded` opens none, but it can notify.
- **Each incident** records the monitor, the first failing result, when it opened, who acknowledged it and when, and when and how it was resolved.
- **Resolving:** an incident resolves on its own when the monitor comes back `up`. A person can also resolve it by hand, which suppresses new incidents until the monitor has been `up` once.
- **Acknowledging** stops repeat notifications. Without it, a reminder goes out every 30 minutes by default, or never.
- **The timeline** shows every state change, notification and acknowledgement, with who did it.

### Alerts

**Alert channels** belong to a workspace, so its projects share them, and its owners and admins manage them:
- **Slack** and **Microsoft Teams:** an incoming webhook URL, from Slack's incoming webhooks or Teams' Workflows app. The message says what's down, since when and why, and links to the incident.
- **PagerDuty:** an Events API v2 integration key. Incidents trigger, acknowledge and resolve the PagerDuty incident through a dedup key.
- **Webhook:** any HTTPS URL, receiving JSON signed as [Standard Webhooks](https://www.standardwebhooks.com/) describe, with a secret Signalbox generates.

Email needs SMTP settings, and Signalbox has avoided needing them so far ([ADR-0012](../adr/0012-invitation-links.md)). It comes before 1.0, with the SMTP settings.

A channel's URL or key is a credential: it's stored encrypted with the workspace key, like secret variables ([ADR-0029](../adr/0029-environment-variables.md)), and never returned. So alert channels need `SIGNALBOX_SECRET_KEY`. A Send test action posts a test message.

**Delivery** goes through an outbox:
- The state change and the notification rows are written in one transaction, so a notification is never lost or sent for a change that didn't happen.
- The worker picks up notifications within a second, by `LISTEN`/`NOTIFY` with a polling fallback, and posts them through the egress policy.
- A failed post is retried with backoff for up to 24 hours. Each attempt's status and error show on the incident's timeline.

**The two-minute budget**, for a monitor at a 1-minute interval:

| Step | Worst case |
|---|---|
| The failure starts just after a check | 60 s |
| The first check times out | 30 s (the default timeout) |
| The confirmation, 10 s later, times out | 40 s |
| The worker posts to Slack | 1 s |

That's 131 seconds at worst with the default timeout, so the demo's health check uses a 10-second timeout: 91 seconds. A shorter timeout, or a check that fails fast, is the usual case.

### Tracing

**Receiving spans:**
- **OTLP over HTTP:** `POST /v1/traces` on `:4318`, protobuf or JSON, gzip or not.
- **OTLP over gRPC:** on `:4317`.
- **Zipkin v2:** `POST /api/v2/spans` on `:9411`, JSON or protobuf. B3 IDs map to W3C Trace Context IDs as Zipkin defines.

These are ingest's own listeners, not the API's. Each answers as its protocol defines, including `429` or `RESOURCE_EXHAUSTED` with a retry delay when it's overloaded, which OpenTelemetry exporters respect.

**Ingest keys:**
- **Scope:** a key belongs to one project environment, and every span sent with it lands there, whatever its attributes say. One key can't write to another environment.
- **Format:** keys look like `sbx_ing_…`, are shown once and stored as SHA-256 hashes, like personal access tokens ([ADR-0026](../adr/0026-personal-access-tokens.md)).
- **Sending:** clients send the key as `Authorization: Bearer …`, which every OpenTelemetry exporter can set through `OTEL_EXPORTER_OTLP_HEADERS`.
- **Management:** project owners and admins create and revoke keys. Each key records when it was last used.

**Writing:**
- Spans are written in batches of up to a second or a few thousand spans, as ADR-0014 measured.
- A request is acknowledged only once its spans are stored, so an acknowledged span is never lost. Exporters send in the background, so the second's delay costs them nothing.
- The buffer is bounded. When ClickHouse falls behind, ingest answers "retry later" rather than grow.

**Before storing, ingest:**
- **Links spans to services:** a span's `service.name` matches a canvas service by the service's telemetry name, which the services table already has, or its slug. Spans that match no service are stored anyway and offered on the canvas as services to add.
- **Bounds names:** a server span with an HTTP route gets the operation name `METHOD /route`, as ADR-0014 asks. A service's operations beyond 1,000 distinct names fold into one `(other)` operation in the per-minute rollup. The span keeps its own name.
- **Redacts credentials:** values of attributes named like credentials (`authorization`, `cookie`, `set-cookie`, `*token*`, `*secret*`, `*password*`, `*api-key*`, `*apikey*`) become `•••`. So do such query parameters in URL attributes. Database statements are kept as sent; replacing their literals comes with the drift work before 1.0.
- **Enforces limits:** at most 128 attributes per span, 4 KiB per value, 128 events and 128 links per span, as OpenTelemetry's SDK defaults are. Requests are at most 16 MiB once decompressed. Each environment also has a rate limit, `SIGNALBOX_INGEST_SPANS_PER_SECOND`, 5,000 by default.

**Reading:**
- **Search:** by service, operation, status, duration, time range and `key=value` attributes, newest first, as ADR-0014 measured.
- **Trace view:** a trace by its ID, as a waterfall. Each span shows its attributes, events and links, the critical path is marked, and spans that refer to a parent Signalbox hasn't received say so.
- **Metrics per service and operation:** rate, error rate and p50, p95 and p99 latency over a time range, from `span_metrics_1m`.

**Open trace:**
- The runner sends a W3C `traceparent` header with every request, unless the request sets its own, so a traced API continues the trace.
- History entries, run steps and check results keep the trace ID, and link to the trace once its spans arrive.
- The runner doesn't send spans of its own in v0.3.

### The service graph

A line from service A to service B means A's client spans have B's server spans as children.

**Building it:** every minute, the worker builds the minute that ended 30 seconds earlier into `service_edges_1m`. It joins that minute's spans with their parents in ClickHouse, by trace and span ID, with a look-back for parents that started earlier. Each line keeps calls, errors and a latency histogram per minute, like `span_metrics_1m`.

**Why in the worker, not at ingest:** joining at ingest only works when a trace's spans reach the same process. A join in ClickHouse works however many ingest processes there are, and a minute's late spans still pair.

**Databases and hosts nobody instruments:** client spans with no child server span still draw a line:
- to a **database**, named by `db.system` and the server address;
- to an **external host**, named by `server.address`.

These appear on the canvas as nodes of their own, from the data alone.

**Measuring it:** ADR-0014 didn't measure this. The first pull request that builds the graph measures it with `tools/spanbench` at 5,000 spans a second, and records the result in its ADR.

### The live canvas

**Transport:** the canvas asks `GET /api/v1/projects/{projectId}/canvas/live?environment=…` every 5 seconds, the rate [ADR-0018](../adr/0018-live-canvas.md) planned for.
- **What it returns:** for each service, its monitors' state, health components, rate, error rate and p95 latency over the last 5 minutes. For each line, its rate and error rate.
- **What it sends:** only what changed since the ETag the browser sends, and `304` when nothing did.
- **Caching:** the server caches the answer per environment for 5 seconds, so 50 people watching one project cost one ClickHouse query each time.

**On the canvas:**
- Services show the values from ADR-0018's `LiveMetrics` store, health as a shape and a word.
- Lines drawn from traffic are solid, and the dependencies people declare by hand are dashed. Declared dependencies are new in v0.3: editors draw them between services.
- Databases and external hosts from the graph are placed by the automatic layout. Moving one saves its position, as for services.
- Services seen in spans but not on the canvas are listed with Add.

**The environment switcher** chooses which environment's traffic and health to show.

### The demo shop

**What it is:** a few services in `examples/demo-shop`, its own Go module, so its OpenTelemetry SDK dependencies never reach Signalbox's binaries:
- a storefront with a traffic generator;
- `orders-api`, which has its own PostgreSQL;
- `inventory-api`;
- `payments-api`, which calls a fake payment provider.

**What each service has:** an OpenAPI spec, a health endpoint in `application/health+json`, and OpenTelemetry instrumentation that sends to Signalbox over OTLP.

**Breaking it on purpose:** a switch makes payments slow, failing or unhealthy, so the canvas, monitors and alerts have something to show.

**Running it:** `deploy/compose/compose.demo.yaml` adds it to the stack. A seeding step uses the public API to create the project, its services and specs, an ingest key and the monitors.

**Testing with it:**
- The end-to-end tests use the demo shop: spans reach a trace, a line appears on the canvas, and a broken health check opens an incident and posts to a webhook receiver within the budget.
- Slack's message format is covered by unit tests. The maintainer checks the done-when condition against a real Slack workspace before tagging v0.3.
- ADR-0014 asked for the demo shop's real spans to be measured with `tools/spanbench` before v0.3. That measurement goes in the demo shop's pull request.

### API changes

New endpoints, added to `api/openapi.yaml` as they're built:
- **Monitors:**
  - `/api/v1/projects/{projectId}/monitors` and `/api/v1/monitors/{monitorId}`;
  - `/api/v1/monitors/{monitorId}/checks` for results;
  - `POST /api/v1/monitors/{monitorId}/check` to check now.
- **Heartbeats:** `POST /api/v1/heartbeats/{token}`, which needs no sign-in, since the token is the credential. `GET` works too, for cron jobs that only have `curl`.
- **Incidents:**
  - `/api/v1/projects/{projectId}/incidents` and `/api/v1/incidents/{incidentId}`;
  - `POST /api/v1/incidents/{incidentId}/acknowledge` and `POST /api/v1/incidents/{incidentId}/resolve`.
- **Alert channels:**
  - `/api/v1/workspaces/{workspaceId}/alert-channels` and `/api/v1/alert-channels/{channelId}`;
  - `POST /api/v1/alert-channels/{channelId}/test`.
- **Ingest keys:** `/api/v1/environments/{environmentId}/ingest-keys` and `/api/v1/ingest-keys/{keyId}`.
- **Traces:**
  - `/api/v1/environments/{environmentId}/spans` for search;
  - `/api/v1/environments/{environmentId}/traces/{traceId}`;
  - `/api/v1/environments/{environmentId}/services` and `/api/v1/environments/{environmentId}/operations` for metrics;
  - `/api/v1/environments/{environmentId}/graph`.
- **Canvas:**
  - `/api/v1/projects/{projectId}/canvas/live`;
  - `/api/v1/projects/{projectId}/dependencies` for the lines people draw.
- **Changed:** history entries, run steps and check results gain `traceId`.

**Who may do what:**

| | Owners and admins | Editors | Viewers |
|---|---|---|---|
| Read monitors, incidents, traces and the canvas | yes | yes | yes |
| Create, change and pause monitors; acknowledge and resolve incidents | yes | yes | no |
| Create and revoke ingest keys | yes | no | no |
| Manage the workspace's alert channels | yes | no | no |

### The web app

- **Monitors:** a project's Monitors page lists them with state, uptime and response time. A monitor's page shows its recent results, incidents and settings, and its form builds the request with the request editor's parts.
- **Incidents:** a project's Incidents page lists open incidents first. An incident's page shows the timeline, with Acknowledge and Resolve.
- **Traces:** a project's Traces page has the search, results with each trace's duration and services, and the waterfall.
- **Canvas:** the canvas goes live. A service's side panel gains its monitors, its open incident, its metrics and a link to its traces.
- **Settings:** workspace settings gain Alert channels, and project settings gain Ingest keys, with the OpenTelemetry settings to copy.

## Security and privacy

- **Ingest is a new public surface:**
  - **Keys:** only requests with a valid key are read past their headers.
  - **Decompression:** gzip and zstd are read through bounded readers, so a small request can't decompress into gigabytes.
  - **Parsing:** protobuf and JSON are parsed with size limits.
  - **Limits:** the per-span limits above and the rate limit per environment apply.
  - **Ports:** the listeners are separate, so an operator can expose them only inside the network that sends spans.
- **Spans can hold personal data**, like the APIs they describe:
  - **Redaction:** credentials are redacted at ingest, as above.
  - **Who sees them:** every member of the project, as for test runs.
  - **Retention:** 7 days by default, and spans are deleted with their environment.
- **Monitors send requests** like the client does, through the egress policy. A monitor of an internal service needs `SIGNALBOX_EGRESS_ALLOW` to reach it. Monitors use the environment's secrets, decrypted only in the runner, and their results keep masked values only.
- **Alert channels** hold credentials (webhook URLs and keys), which are encrypted and write-only. Posts to them go through the egress policy, so a webhook can't reach Signalbox's own services. A webhook's signing secret is shown once.
- **Heartbeat tokens** are long, random, stored as hashes, and rate-limited per token. A leaked one lets someone keep a monitor up or make it fail, nothing more, and it can be rotated.
- **The `traceparent` header** the runner sends carries random IDs only. It tells the called API that the request comes from a traced caller, which Postman's requests don't. A request can set its own header, or an empty one, to opt out.

## Operations

- **Migrations:**
  - **PostgreSQL:** new tables in `monitors`, `alerts` and `telemetry`'s schema: `monitors`, `incidents`, `incident_events`, `alert_channels`, `monitor_alert_channels`, `notifications`, `ingest_keys` and `canvas_dependencies`.
  - **ClickHouse:** `check_results` and `service_edges_1m`, and the spike's tables, through the new migrations.
- **Upgrading from v0.2:** ClickHouse must be configured before upgrading. The Compose stack already runs it, and the release notes say so.
- **New settings:**
  - `SIGNALBOX_CLICKHOUSE_URL`, now read;
  - `SIGNALBOX_OTLP_GRPC_ADDR`, `SIGNALBOX_OTLP_HTTP_ADDR` and `SIGNALBOX_ZIPKIN_ADDR`, `:4317`, `:4318` and `:9411` by default, empty to turn one off;
  - `SIGNALBOX_TRACE_RETENTION_DAYS` and `SIGNALBOX_INGEST_SPANS_PER_SECOND`.
- **Ports:** the Compose stack and the image expose `4317`, `4318` and `9411`.
- **Dependencies:**
  - **What's new:** OTLP needs `google.golang.org/protobuf`, the OTLP definitions in `go.opentelemetry.io/proto/otlp`, and `google.golang.org/grpc` for `:4317`. They're Apache-2.0 and BSD-3-Clause, maintained by Google and the OpenTelemetry project, and gRPC is a large tree, which is why [ADR-0008](../adr/0008-dependency-policy.md) asks for this RFC.
  - **Measuring:** the gRPC pull request records what they add to the binary.
  - **Zipkin:** it needs no new dependency. Its JSON is small, and its protobuf is read with `protowire`.
- **Resources:**
  - **Spans:** about 24 GB of disk a day at 5,000 spans a second, 170 GB for the default week (ADR-0014). The installation guide gains a sizing table.
  - **Monitors:** 1,000 monitors at a 1-minute interval is 17 checks a second, well within one runner.
- **Observability:** Signalbox has no metrics endpoint yet, and v0.3 needs one: an operator must see spans received, refused and dropped, how far ingest is behind, check start delay, and notifications failing. See the first unresolved question.

## Alternatives

- **Pushing live updates over WebSocket or Server-Sent Events.** Deferred, as for runs (ADR-0033) and the agent ([ADR-0037](../adr/0037-local-agent.md)). A 5-second poll with ETags is what ADR-0018 planned for, works through every proxy, and costs one cached query per environment. A stream can replace it without changing the canvas, which reads `LiveMetrics` whatever brings the data.
- **Require an OpenTelemetry Collector in front of Signalbox,** and accept only OTLP over HTTP from it. That saves the gRPC dependency, but most teams' SDKs are configured for gRPC by default, and an extra service to run for every installation goes against self-hosting being the product.
- **Store check results in PostgreSQL.** 1,000 one-minute monitors write 1.4 million rows a day, which PostgreSQL holds, but uptime over 90 days means scanning them. ADR-0002 already puts results in ClickHouse, next to the spans they link to.
- **Build the service graph at ingest,** as the OpenTelemetry Collector's service graph connector does. It needs both spans of a call in one process within a time window. The worker's join in ClickHouse doesn't, and costs a query a minute.
- **Store traces in Jaeger, Tempo or Zipkin's own storage.** Rejected in ADR-0002 and ADR-0014: Tempo is AGPL, and the others make attribute queries and drift analysis harder than ClickHouse.
- **Run monitors on agents.** Agents are personal and often asleep; a monitor that stops when a laptop closes isn't a monitor. Locations run by the installation come with NATS.
- **Wait for NATS before the scheduler and worker.** They coordinate through PostgreSQL rows, as runs already do, so NATS isn't needed for correctness, only to run roles apart.

## Unresolved questions

1. **Metrics endpoint:** should v0.3 add `/metrics` in the Prometheus text format to the ops listener? Proposed: yes, as the first pull request, written by hand rather than with `prometheus/client_golang`, which is a large tree for a text format. The deferred metrics from RFC 0001 (requests run, script time, connected agents) join it.
2. **Confirming from a second location:** proposed for before 1.0, with NATS and runners in more than one place. In v0.3 a failure is confirmed by a second check from the same runner.
3. **Retention per project:** proposed for before 1.0. ClickHouse can expire rows by a column, but whole days can then no longer be dropped at once, which needs measuring.
4. **Zipkin clients that can't set headers:** proposed to send through an OpenTelemetry Collector or a proxy that adds the key. Accepting keys in the URL would put them in access logs.
5. **ClickHouse required:** proposed yes. A small installation that only wants monitors still needs it, about 2 GB of memory with the small-machine profile.
6. **Email alerts:** proposed for before 1.0, with SMTP settings that invitations can use too.
7. **The order of work,** proposed as these pull requests:
   1. the metrics endpoint;
   2. ClickHouse in production, with its migrations and readiness;
   3. ingest keys and the ingest role, with OTLP over HTTP and Zipkin;
   4. OTLP over gRPC;
   5. the demo shop, with spans measured;
   6. trace search and the waterfall, and `traceparent` with Open trace;
   7. the service graph and metrics per service and operation;
   8. the live canvas and declared dependencies;
   9. monitors: the scheduler, HTTP and health checks, results;
   10. collection monitors and heartbeats;
   11. incidents;
   12. alert channels and delivery.
