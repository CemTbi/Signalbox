# 0041. OTLP over gRPC, without the generated service

- Status: Accepted
- Date: 2026-10-30
- Deciders: project lead

## Context

[ADR-0040](0040-span-ingest.md) receives spans over OTLP/HTTP and Zipkin. Most OpenTelemetry SDKs and the Collector default to OTLP over gRPC on port 4317, so [RFC 0002](../rfcs/0002-monitors-and-tracing.md) includes it, and accepted `google.golang.org/grpc` as a dependency for it.

The usual way to serve OTLP/gRPC is to implement the generated `TraceServiceServer` from `go.opentelemetry.io/proto/otlp/collector/trace/v1`. That package also holds the generated grpc-gateway handlers, so importing it links grpc-gateway's runtime into the binary too, for a REST mapping Signalbox already serves itself.

## Decision

**The ingest role serves OTLP/gRPC on `:4317`** (`SIGNALBOX_OTLP_GRPC_ADDR`, empty to turn it off), alongside OTLP/HTTP and Zipkin. `--roles=all` includes it.

**The service is registered by hand:**
- **Service:** one `grpc.ServiceDesc` for `opentelemetry.proto.collector.trace.v1.TraceService`, with its one method, `Export`.
- **Codec:** the server uses a codec named `proto` that passes messages through as bytes. A request reaches the handler as bytes, read as `TracesData` like OTLP/HTTP's protobuf, and the answer leaves as the bytes of an `ExportTraceServiceResponse`, encoded with `protowire`.
- **Imports:** neither the collector package nor grpc-gateway is imported outside tests. The tests call the server with OTLP's own generated client, so it's proven to work with what exporters use.

**The same pipeline as OTLP/HTTP:**
- **Shared steps:** key check, the per-address limit on unknown keys, decoding, limits, redaction, the rate per environment, and acknowledging only stored spans all run through the same code (`authenticate` and `accept`).
- **Keys:** a key arrives in the `authorization` metadata, as `OTEL_EXPORTER_OTLP_HEADERS` sends it.

**Status codes follow the OTLP specification:**
- **Unauthenticated:** no key, or an unknown one.
- **InvalidArgument:** a message that isn't OTLP.
- **ResourceExhausted:** an environment over its rate, or a message over 16 MiB. A rate refusal carries a `RetryInfo` detail with the delay, since exporters retry `RESOURCE_EXHAUSTED` only when it does.
- **Unavailable:** a full writer, or ClickHouse failing, with `RetryInfo`.
- **Partial success:** some spans refused, as in OTLP/HTTP.

**Server settings:**
- **Size:** messages are at most 16 MiB, the same limit as HTTP bodies.
- **Compression:** gzip is registered, which exporters may use. zstd isn't: no exporter sends it by default over gRPC.
- **Streams:** at most 1,000 concurrent streams per connection.
- **Keepalive:** pings every 2 minutes, idle connections closed after 5 minutes, and clients kept from pinging more often than every 10 seconds.
- **Stopping:** shutdown stops gRPC gracefully within the shutdown timeout, before the writer writes what it holds.

**Metrics:** gRPC requests count under `protocol="otlp_grpc"` in the ingest metrics.

## Consequences

- Exporters work with Signalbox with their default protocol: set `OTEL_EXPORTER_OTLP_ENDPOINT=http://signalbox:4317` and the key in the headers.
- The binary gains gRPC and `genproto/googleapis/rpc`, which gRPC itself needs, but not grpc-gateway. The Compose stack and the image expose port 4317.
- If OTLP adds methods that Signalbox wants, such as for logs or metrics, each is another `MethodDesc`, the same way.
- A raw-bytes codec means the server never validates messages against a schema before the handler does. The handler decodes with `proto.Unmarshal` under the size limit, as OTLP/HTTP does.

## Alternatives considered

- **Implement the generated `TraceServiceServer`.** It's the conventional route, but it links grpc-gateway's runtime for no use.
- **Generate our own service code from the OTLP `.proto` files.** That needs `protoc` and plugins in the build, and generated code to keep in step with the definitions. One method doesn't need that.
- **Accept only OTLP/HTTP, and tell people to use a Collector for gRPC.** RFC 0002 rejected that: gRPC is most exporters' default, and an extra service for every installation goes against self-hosting being the product.
