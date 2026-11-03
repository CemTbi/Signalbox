# 0045. The canvas goes live: polling, changes and declared lines

- Status: Accepted
- Date: 2026-11-03
- Deciders: project lead

## Context

[ADR-0018](0018-live-canvas.md) showed the browser can keep 200 live nodes smooth when metrics reach them through `LiveMetrics`, every 5 seconds. [ADR-0044](0044-service-graph.md) built the lines. [RFC 0002](../rfcs/0002-monitors-and-tracing.md) asked for a polled endpoint that sends only what changed, a 5-second server cache per environment, and lines people declare by hand. The RFC left open where the code lives, what stays on the canvas once traffic stops, and how a database nobody has placed finds a spot.

## Decision

**A `canvas` module answers what's live.** It reads the services from `apis` and the traffic from `telemetry`, and matches them. Neither of those knows the other. What people decide about the canvas, the places of databases and hosts and the dependencies they declare, is stored by `apis` beside the services' own positions, so one move of several kinds of node is one transaction.

**`GET /api/v1/projects/{projectId}/canvas/live?environment=…`**
- **Services:** each canvas service, and each service seen in spans that matches none, with its requests a second, error rate and p95.
- **Lines:** between node IDs, with calls a second and error rate. A database's or host's node ID is its kind and address, such as `database:shop-db`. A service not on the canvas is `seen:` and its name.
- **Not yet:** health and monitors' state arrive with monitors. Until then the canvas says "No checks", which is true.

**Time windows:**
- **Service rates** cover the last 5 minutes from `span_metrics_1m`, whose newest minute is still filling. They're divided by the time since the oldest minute began, so the rate is right throughout the minute.
- **Line rates** cover the 5 whole minutes the graph job has built, ending about 2 minutes ago. A line reacts two minutes after a service does, as ADR-0044 said.
- **Staying:** a service or line stays for an hour after its last traffic, at a rate of 0, so a quiet spell doesn't empty the canvas and move what's on it.

**Matching happens at read time,** so setting a service's telemetry name takes effect at once, for spans already stored.
- A service answers to its telemetry name, or to its slug if it has none. Having told it a name, its slug no longer matches.
- If two services claim a name, a told name beats a slug, and otherwise the oldest wins. The other shows no traffic.
- A call to an address that a canvas service answers to is a call to that service, even before it sends spans.
- Calls that resolve to one pair of nodes are one line. Calls that resolve to a node calling itself aren't lines.

**Changes, not everything:**
- **The ETag** is a hash of the state, so every API process gives one state one tag.
- **`If-None-Match`** gets `304` if that's still the state, the changes if the process still knows that state, and everything otherwise.
- **Known states:** each process keeps an environment's last 12 states, a minute's worth. A browser that was away longer, or asks another process, gets everything, which is always correct.
- **`changesSince`** says which kind of answer it is, so nothing relies on the status alone. A body that depends on the request's `If-None-Match` is `Cache-Control: no-store`.
- **Rounding:** rates and p95 keep 3 significant digits, and error rates 4 decimals. More would be noise that makes every poll a change, and it isn't measurable on a node.

**The cache** holds an answer per environment for 5 seconds, per process, and one request reads while others wait.
- **Access** is checked on every request, before the cache.
- **Failures** are cached for 5 seconds too, so people watching don't queue behind a ClickHouse that's down. Someone leaving mid-read isn't a failure.
- **Idle environments** are forgotten after 2 minutes.
- **Bounds:** each read returns at most 5,000 services and 5,000 lines, busiest first, in case spans carry made-up names.

**The canvas:**
- **Values** reach nodes and lines through `LiveMetrics`. React Flow's graph changes only when the set of nodes and lines does.
- **Polling** pauses while the tab is in the background, and the next poll is the retry.
- **The environment switcher** remembers the choice per project in the browser, and starts on production.
- **Lines** are solid from traffic and dashed when declared. Traffic takes a declared line over, since both are named by their two nodes. A failing line says its error rate in words.
- **Databases and hosts** show the calls to them, added up. They have no p95 of their own, since the callers' durations include the network, and no checks yet.

**A new database or host goes beside what calls it:** to the right of its callers, level with them, and below anything already there. A layout of the whole canvas gives a better picture, but it would move what people placed by hand, so ELK runs only when someone asks with Arrange automatically, which now knows the lines. The place is saved when someone moves the node or arranges the canvas.

**Declared dependencies** go between services only, one each way, at most 1,000 in a project. Editors drag between two services' edges, or choose them in a form, since dragging can't be the only way (WCAG 2.2 success criterion 2.5.7). They're removed from the list under the canvas: a line is too small a target to select and delete.

**Backspace no longer removes a node** from the canvas's view, which React Flow does by default. It looked like deleting a service, and didn't.

## Consequences

- Fifty people watching one project cost one read of ClickHouse every 5 seconds, per API process. The read touches the last hour of two rollups.
- With steady traffic, most polls change something, so `304` mostly helps quiet projects. Sending changes still saves the services whose values held.
- A database whose address differs between callers is two nodes. Traces name it that way.
- A rate read in the first 5 minutes after traffic starts is low, because the window is averaged over time with nothing in it.
- A lagging graph job makes line rates fall towards 0 rather than show stale numbers. Its metrics say it's behind (ADR-0044).
- `PUT /canvas` takes either kind of node, so `positions` is no longer required, which older clients don't notice.

## Alternatives considered

- **Server-sent events.** ADR-0018 left the transport open. Polling with ETags works through every proxy, costs no open connection per viewer, and 5 seconds is what the RFC asks for.
- **Always send everything.** Simpler, but a canvas of 200 services and 284 lines is about 85 KB every 5 seconds, adding up its fields. The RFC accepted the changes, and they're a small part of the code.
- **Whole minutes only.** Values would change once a minute, so `304` would be common, but a failing service would take a minute to show.
- **Lay out new nodes with ELK and keep only their positions.** It places them against a layout nobody is looking at, so they land on top of nodes people moved.
- **Store the databases and hosts as services.** They'd need creating, and would outlive the traffic that named them. A row here holds only a place.
