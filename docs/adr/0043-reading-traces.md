# 0043. Reading traces, and opening a request's trace

- Status: Accepted
- Date: 2026-10-31
- Deciders: project lead

## Context

Since [ADR-0040](0040-span-ingest.md) and [ADR-0041](0041-otlp-grpc.md), services send spans to Signalbox, but nobody can see them. [RFC 0002](../rfcs/0002-monitors-and-tracing.md)'s sixth step makes them readable:
- **Search:** find traces by service, operation, status, duration, time range and attribute.
- **Waterfall:** a trace as a waterfall with span details and the critical path.
- **Open trace:** from a request Signalbox sent, through a `traceparent` header.

[ADR-0014](0014-span-storage.md) measured the queries this needs: a trace by ID, and searches that reach back from the newest five minutes.

## Decision

**Search finds traces by their spans.**
- **Matching:** a trace matches when one of its spans passes every filter. Filters are service, operation, error status, a minimum duration, and an attribute as `key=value`.
- **Results:** up to 100 traces, 20 by default, newest first.
- **Each trace's summary:** start, duration, span and error counts, its services, and the span that started it.
- **How it reads:** a search reads spans newest first, as ADR-0014's search does. The matched traces are then summarized in one query, bounded by `trace_ids` to the seconds they cover.
- **Time range:** one search covers at most 31 days, the last hour by default.

**A trace opens from its project.** `GET /api/v1/projects/{projectId}/traces/{traceId}` looks in every environment of the project. A request's trace lands in whichever environment the services it called send to, which the request doesn't know. The answer names that environment.

**Every member of the project may read its traces**, as they may its test runs. Credentials were redacted at ingest.

**Services and their traffic:**
- **The endpoint:** `GET /api/v1/environments/{environmentId}/services` lists the services that sent spans in a time range, each with its calls, errors and latency.
- **What counts:** a service's server and consumer spans only, from the per-minute rollup. A service's client spans are other services' requests, and counting them would count each call twice.
- **What uses it:** the search's service filter now. The canvas will later.

**Requests are sent in a trace:**
- **The header:** the client engine adds a W3C `traceparent` to every request it sends: from the web app, collection runs, the CLI's `test run`, and through agents. It uses a new random trace ID and the sampled flag, so an API that is traced records the trace.
- **Precedence:** it adds the header after pre-request scripts and auth, so a request that sets its own `traceparent` keeps it and joins that trace.
- **Opting out:** a request that sets an empty `traceparent` opts out, and the empty header isn't sent.
- **Recording:** history entries and run steps keep the trace ID in their stored records, so no migration is needed. The API returns it as `traceId`.
- **Not traced:** `pm.sendRequest` from scripts isn't traced.
- **No spans of its own:** Signalbox sends no span for the request, as RFC 0002 decided. So the trace's first span names a parent that never arrives, and the waterfall says "Parent not received" rather than treating it as a fault.

**The web app:**
- **The Traces page:** a project's Traces page searches one environment, production by default, with the filters in the page's address, so a search can be shared.
- **The trace page** shows:
  - a waterfall: spans in tree order, each child under its parent by start time;
  - each service in its own color, always named beside it;
  - each span's duration beside its name;
  - the critical path outlined;
  - details for the chosen span: attributes, resource, events, links and instrumentation.

  Spans whose parent hasn't arrived start trees of their own.
- **The critical path** walks back from a span's end. The child that finished last is what it waited for. Before that child started, it's the one that finished last before then, and so on, as Jaeger computes it.
- **Spans still arriving:** each service exports in batches every few seconds, so a trace that ended under 30 seconds ago may still gain spans. The trace page says so and asks again every 5 seconds until then.
- **Open trace** appears beside a response, and beside a run step.

## Consequences

- People can follow a request from the client through every service it touched, the database included, as the demo shop shows.
- APIs Signalbox calls now receive a `traceparent` header, which Postman's requests don't send. A traced API joins Signalbox's trace. The empty header opts a request out.
- A trace whose services all send to environments outside the project can't be opened from it. Traces belong to the project whose ingest key received them.
- Spans of deleted environments stay until retention drops them. ADR-0040 noted this; deleting them belongs with environment management.

## Alternatives considered

- **Look a request's trace up only in the environment it was sent with.** Requests are often sent without an environment, and services send to the environment of their deployment, not of the request.
- **Send a span for each request from the runner,** so traces have their true root. Deferred in RFC 0002. It needs the runner to write spans, and a choice of environment for them.
- **Stream search results.** Searches answer in tens of milliseconds (ADR-0014), so a page of results doesn't need streaming.
- **Compute the critical path on the server.** It's a few dozen lines over spans the browser already has, and the API stays a plain list of spans.
