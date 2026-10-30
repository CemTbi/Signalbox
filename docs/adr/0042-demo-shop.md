# 0042. The demo shop

- Status: Accepted
- Date: 2026-10-31
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) says v0.3 is done when "the demo shop shows live traffic on the canvas, and a failing health check reaches Slack within two minutes". The demo shop must:
- give people trying Signalbox something real to look at, without instrumenting their own services first;
- give the end-to-end tests real traffic: traces, a service graph, health checks to watch and break;
- measure what real OpenTelemetry instrumentation costs in storage. [ADR-0014](0014-span-storage.md) asked for that before v0.3, since its numbers came from synthetic spans.

## Decision

**A Go module of its own**, `examples/demo-shop`, so the OpenTelemetry SDK and its dependencies never reach Signalbox's binaries or its license check. CI vets, lints and tests it, and lints its specs with `--fail-on warning`, as Signalbox's own spec is.

**One binary, one image, a part per container:**

| Part | Role |
|---|---|
| `storefront-web` | The web API browsers call; passes requests on. |
| `orders-api` | Reserves stock, charges, and saves orders in PostgreSQL. |
| `inventory-api` | Products and stock, in memory; restocks what runs out. |
| `payments-api` | Charges through the payment provider; has the chaos switch. |
| `psp` | The payment provider, outside the shop and not traced, so Signalbox sees it only as an external host. |
| `shopper` | Browsing and buying, three visits a second; untraced, like browsers, so traces start at the storefront. |
| `seed` | Sets the shop up in Signalbox, then exits. |

**What the instrumentation sends:**
- **Exporters:** the OpenTelemetry Go SDK sends over OTLP/gRPC from three services and OTLP/HTTP from payments, so both protocols carry real traffic.
- **Server spans** are named after their route and carry `http.route`.
- **Database spans:** a small pgx tracer makes a client span per query, with `db.system.name`, `db.query.text` and `server.address`, so the database appears on the service graph. Queries outside a trace, such as health checks, make none.
- **Health checks** aren't traced, so they don't drown the shop's own spans.

**Health:**
- **Format:** every service answers `GET /health` in `application/health+json`, with its components: the database's response time, the payment provider's, and the stock count.
- **The chaos switch:** payments' `PUT /chaos` takes four modes.
  - `slow` adds 1.5 s to each payment.
  - `failing` fails half of them.
  - `unhealthy` fails the health check while payments still work.
  - `off` mends them.

  The switch is what RFC 0002's done-when condition breaks on purpose.

**The seed uses the public API**, as any client would, and as principle 5 of the requirements asks:
- **Signing in:** it signs in like the web app does: a session cookie, the public URL as `Origin`, and the session's CSRF token.
- **First-run setup:** with `DEMO_SETUP_CODE` it completes setup itself. Without it, it waits for someone to complete it. The end-to-end stack relies on that: the tests set up the installation, and the seed follows.
- **What it makes:** the Demo shop workspace, the Shop project, the four services placed on the canvas with their telemetry names, specs published as 1.0.0, and an ingest key for production. It finds what exists before making anything, so running it again changes nothing but the key.
- **The key:** each run revokes its earlier key and makes a new one.

**Services follow the key file:**
- **Reading:** the seed writes the key to a shared volume. Services read it whenever they export, re-reading the file when it changes: as gRPC per-call credentials, and as an HTTP transport.
- **Starting:** services start at once. Their first spans are refused until the key is written, and a new key reaches them without a restart.
- **Waiting:** `docker compose up --wait` doesn't hang on a seed that waits for setup.

**The end-to-end stack includes the demo.**
- **CI:** the e2e job builds the shop's image and adds `compose.demo.yaml`.
- **The test** waits for the seed to set the shop up after the tests complete setup. It then checks the services, that the shop's ingest key has been used, and that the shop and its health checks answer.

## Spike follow-up: real spans (2026-10-31)

The demo ran in Compose on the same machine as ADR-0014's spike, with three visits a second.

| | Demo shop |
|---|---|
| Spans | 11,777 in nine minutes, about 22 a second, from four services |
| Attributes per span | 9 to 10 on server spans, 3 to 4 on client spans |
| Stored data | 36 bytes a span compressed, 263 uncompressed |
| Text index on attributes | 4.5 bytes a span |
| In all | 41 bytes a span |

The spike measured 55 bytes a span on 216 million synthetic spans. The demo's spans are fewer and more alike, which compresses better. But they are the attributes real instrumentation sends: client addresses and ports, paths with IDs, user agents. Nothing in them suggests the spike's per-span cost was too low.

## Consequences

- Anyone can see Signalbox with traffic in one command. The README shows how to break payments.
- The end-to-end job takes longer: one more image to build, and about 20 seconds more of tests.
- Later steps reuse the demo:
  - the trace view gets real traces;
  - the service graph gets its lines, the database, and an external host;
  - monitors get health checks, and alerts a switch that fails them.
- The demo's spans are only a sample. A larger measurement, with more services and hours of data, still belongs to `tools/spanbench` against a real installation.

## Alternatives considered

- **A demo in another language, such as OpenTelemetry's own demo.** It's larger and more varied, but it needs a dozen containers and gigabytes of memory, and isn't ours to change for tests.
- **Seeding through the database.** Faster, but it would skip the API's checks and couple the demo to the schema. Using the public API proves it can do what the web app does.
- **Services that wait for the seed before starting.** That deadlocks `up --wait` in the e2e stack, where the seed waits for the tests. A key a service would read once could also be the stale one from an earlier run.
