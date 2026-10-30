# The demo shop

A small web shop that sends real traces to Signalbox: for trying Signalbox, and for its own end-to-end tests. People browse and buy a few times a second, and payments can be broken on purpose, so the canvas, monitors and alerts have something to show.

```text
shopper ──▶ storefront-web ──▶ inventory-api
                  │
                  └──────────▶ orders-api ──▶ inventory-api
                                    │ ──────▶ payments-api ──▶ psp.example (outside the shop, not traced)
                                    └───────▶ PostgreSQL
```

| Part | What it does |
|---|---|
| `storefront-web` | The shop's web API, which browsers call. It passes requests on. |
| `orders-api` | Places orders: reserves stock, charges the total, and saves the order in PostgreSQL. |
| `inventory-api` | Products and their stock, in memory. What runs out is restocked. |
| `payments-api` | Charges orders through the payment provider, and has the chaos switch. |
| `psp` | A payment provider outside the shop. It isn't traced, so Signalbox sees it only as an external host. |
| `shopper` | People browsing and buying, `DEMO_VISITS_PER_SECOND` times a second (3). Like browsers, it isn't traced. |
| `seed` | Sets the shop up in Signalbox, then exits. |

Every service:
- **sends spans** with the OpenTelemetry Go SDK, over OTLP/gRPC, except payments, which uses OTLP/HTTP, so both are in use;
- **answers `GET /health`** in `application/health+json`;
- **has an OpenAPI spec** in [`specs`](specs), which the seed publishes.

## Run it

From the repository root:

```sh
SIGNALBOX_SETUP_CODE=DEMO-SETUP-CODE-0001 DEMO_SETUP_CODE=DEMO-SETUP-CODE-0001 \
  docker compose -f deploy/compose/compose.yaml -f deploy/compose/compose.demo.yaml up -d --build
```

Open http://localhost:8080 and sign in as `ada@example.com`, with the password `correct horse battery staple`. The Demo shop workspace has the Shop project, with the four services on the canvas, their specs published, and an ingest key for production that the services use.

**The seed:**
- **Setting up:** with `DEMO_SETUP_CODE` set, the seed completes Signalbox's first-run setup itself. Without it, it waits until someone does, then signs in with `DEMO_EMAIL` and `DEMO_PASSWORD`, as the end-to-end tests do.
- **Running it again** reuses what's there, and replaces its ingest key.

## Break it

Payments' chaos switch answers on http://localhost:8091/chaos:

```sh
curl -X PUT -d '{"mode":"unhealthy"}' http://localhost:8091/chaos   # the health check fails
curl -X PUT -d '{"mode":"slow"}' http://localhost:8091/chaos        # each payment takes 1.5 s more
curl -X PUT -d '{"mode":"failing"}' http://localhost:8091/chaos     # half the payments fail
curl -X PUT -d '{"mode":"off"}' http://localhost:8091/chaos         # mended
```

## Work on it

The shop is a Go module of its own, so its OpenTelemetry SDK never reaches Signalbox's binaries:

```sh
cd examples/demo-shop
go test ./...
go run . inventory   # spans go nowhere until OTEL_EXPORTER_OTLP_ENDPOINT is set
```

**Settings for every part:**
- **Exporting:** the standard `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_PROTOCOL` (`grpc` or `http/protobuf`).
- **The ingest key:** `SIGNALBOX_INGEST_KEY`, or `SIGNALBOX_INGEST_KEY_FILE` for a file that holds it.
- **Listening:** `DEMO_ADDR`, `:8080` by default.

Each part's own settings start with `DEMO_` and are read in its file.
