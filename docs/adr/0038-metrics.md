# 0038. Metrics in the Prometheus text format, written by hand

- Status: Accepted
- Date: 2026-10-30
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) adds span ingest, a scheduler and alert delivery. An operator must be able to see:
- spans received, refused and dropped;
- how far ingest is behind;
- how late checks start;
- notifications failing.

Logs can't answer "how many a second" or "how late at p99". [RFC 0001](../rfcs/0001-api-client-and-tests.md) also deferred metrics for requests run, script time and connected agents until there was somewhere to put them. Signalbox has no metrics yet. RFC 0002 accepted adding them as v0.3's first step, written by hand rather than with the Prometheus Go client.

The Prometheus text exposition format is what nearly every collector scrapes: Prometheus, VictoriaMetrics, Grafana Alloy, Datadog's agent and the OpenTelemetry Collector's Prometheus receiver.

## Decision

**`GET /metrics` on the ops listener** serves every metric in the text exposition format, version 0.0.4, as `text/plain; version=0.0.4; charset=utf-8`. The listener already serves `/livez` and `/readyz` and stays off the public network ([ADR-0001](0001-single-binary-with-roles.md)), so metrics need no authentication. The api listener doesn't serve them. Every process serves its own metrics, whatever roles it runs.

**`internal/platform/metrics`** implements the format in under 500 lines, the Go runtime's metrics included:
- **Types:** counters, gauges and histograms, each with optional labels. Gauges and counters can also be read from a function at scrape time, for values another package already keeps, such as the database pool's.
- **Updates** are atomic and take no lock once a label combination exists.
- **Output** is sorted by name and labels, so it's stable.
- **Cardinality:** each metric keeps at most 1,000 label combinations. Past that, new combinations count in one series whose labels all read `_overflow`, so a label with unbounded values can't exhaust memory, and the mistake shows.
- **Nil metrics:** a nil registry makes nil metrics, and a nil metric's methods do nothing. Modules count the same way in tests, in the CLI (which serves no metrics) and in the server, without checks at every call.
- **Mistakes in names panic at registration:** a bad name, a counter not ending in `_total`, a name used twice, or the wrong number of label values. The tests that build the server find them.

**Modules own their metrics.** A module that counts something exposes a `UseMetrics` method, or a `Metrics` type its callers pass on. `internal/app` makes one registry per process and hands it out. Metrics are named `signalbox_<area>_<what>_<unit>`, as Prometheus's naming conventions ask.

**Labels never carry IDs, paths or user input.** HTTP requests are counted by the route pattern that served them, such as `/api/v1/projects/{projectId}`, never by the path. Methods outside the standard ones count as `other`.

**The metrics at this step:**

| Metric | Type | Labels |
|---|---|---|
| `signalbox_build_info` | gauge, always 1 | `version`, `commit` |
| `signalbox_http_requests_total` | counter | `route`, `method`, `code` |
| `signalbox_http_request_duration_seconds` | histogram | `route`, `method` |
| `signalbox_http_requests_in_flight` | gauge | |
| `signalbox_runner_requests_total` | counter | `where` (`server` or `agent`), `outcome` (`response` or the failure code) |
| `signalbox_runner_request_duration_seconds` | histogram | `where` |
| `signalbox_scripts_total` | counter | `event` (`prerequest` or `test`), `outcome` (`ok`, `error`, `timeout`, `memory` or `failed`) |
| `signalbox_script_duration_seconds` | histogram | `event` |
| `signalbox_agents_online` | gauge | |
| `signalbox_test_runs_total` | counter | `status` |
| `signalbox_test_runs_running`, `signalbox_test_runs_queued` | gauge | |
| `signalbox_db_connections_max`, `_in_use`, `_idle` | gauge | |
| `signalbox_db_acquires_total`, `signalbox_db_acquire_waits_total`, `signalbox_db_acquire_wait_seconds_total` | counter | |
| `go_goroutines`, `go_sched_gomaxprocs_threads`, `go_memstats_heap_alloc_bytes`, `go_memstats_sys_bytes`, `go_gc_gogc_percent`, `go_gc_cycles_total`, `go_info`, `process_start_time_seconds` | gauge or counter | |

**About these metrics:**
- **Durations** use one set of buckets, from 5 ms to a minute.
- **Agents and test runs** live in the process that serves them, so those metrics count what that process sees. The runner's duration is what the person waits: for an agent, that includes the wait for the agent to take the request.
- **The Go metrics** keep the names the Prometheus Go client gives them, so existing Go dashboards work.

A scrape configuration for Prometheus:

```yaml
scrape_configs:
  - job_name: signalbox
    static_configs:
      - targets: ["signalbox:9090"]
```

## Consequences

- Operators can graph and alert on Signalbox with the tools they have. The metrics RFC 0002's steps add (ingest, check delay, notifications) join the same registry.
- Signalbox ships no dependency for metrics. It also gets none of the Prometheus client's extras: no protobuf or OpenMetrics output, no exemplars, and no process CPU, memory or file metrics, which differ on every operating system. Container platforms report CPU and memory themselves.
- A label added later must have a bounded set of values. The overflow series catches mistakes, but its counts are no use to anyone.
- One registry per process, with no global, means tests can build as many servers as they like.

## Alternatives considered

- **`prometheus/client_golang`.** The standard choice, but it brings `client_model`, `common`, `procfs` and protobuf with it, for a text format of a few hundred lines. RFC 0002 accepted writing it by hand. If Signalbox ever needs exemplars or native histograms, switching costs only `internal/platform/metrics`, since modules use its small API.
- **The OpenTelemetry metrics SDK with a Prometheus exporter.** It is larger still, and it would add a second way to name and shape metrics next to the one operators scrape.
- **Pushing OTLP metrics to a collector.** It needs a collector that every installation has to run and configure. Scraping is what self-hosters' monitoring already does. OTLP export can be added beside the endpoint later.
- **Serving metrics on the api listener behind a token.** It exposes internals on the public network for no gain. The ops listener exists for this.
