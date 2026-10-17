# 0018. A live canvas of 200 services

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

The canvas is every project's home screen: each service is a node showing its spec version, health, latency, error rate and traffic, and lines show who calls whom ([ADR-0006](0006-frontend-architecture.md) picks React Flow, with ELK.js for automatic layout). The build plan asks it to stay usable with 200 live nodes: metrics arrive every few seconds while people pan and zoom.

Spike 3 had to show whether that holds on ordinary hardware, and what the canvas must do to keep it holding.

## Decision

**Live metrics live outside React Flow.** `LiveMetrics` (`web/app/src/features/canvas/live.ts`) holds each node's health and metrics and each line's rate, and each node and line subscribes to its own entry. When metrics arrive, React Flow's nodes and edges don't change, so React Flow does no work; only the nodes and lines whose values changed re-render. While someone pans or zooms, the canvas holds updates and shows them when the gesture ends.

**Nodes are cheap to update.** Service nodes have a fixed size with `contain: strict`, so a change inside one never lays out another. Line labels are plain SVG text on a pill sized from the text's length; React Flow's own labels measure their text with `getBBox` on every change. Below 55% zoom, nodes show only their name and a health shape. Health is a shape and a word everywhere, never color alone.

**Layout runs in a worker, rarely.** ELK's layered algorithm runs in a web worker, loaded on first use, so the page stays responsive however long a layout takes. Canvas v1 will save positions after a layout or a drag, so ELK runs again only when services appear, not on every visit. ELK stays over dagre because the canvas will group services by team or domain, and ELK lays out nested groups; dagre doesn't.

**Live updates will come every 5 seconds by default.** On a typical machine a 200-node update costs about 1% of one core at that rate.

**A canvas of 200 services opens on groups.** Fitting 200 nodes on one screen means a zoom of about 10%, where nothing is readable at any frame rate. Canvas v1 groups services by team or domain, opens with the groups, and zooms into one.

## Spike results (2026-10-17)

The benchmark (`web/app/bench/canvas.html`, measured by `e2e/bench/canvas.mjs`) draws a generated shop with frontends, gateways, domain services, databases and external hosts: 200 services with 284 lines, or 400 with 597. Every node and line gets new values on every update, a worst case. The page was a production build in Chromium 156 on an AMD Ryzen 5 3600. Chrome's CPU throttling stands in for slower machines: 2× for a modest laptop, 4× for a low-end one.

| 200 services | 1× | 2× | 4× |
|---|---|---|---|
| ELK layout, in a worker | 0.6–1.2 s | 0.7–1.0 s | 0.7–1.2 s |
| dagre layout, on the page | 0.27 s | 0.6–0.9 s | 1.3–2.1 s, page frozen |
| Live update, until painted (two frames included) | 37–45 ms | 85–110 ms | 190–265 ms |
| Frames over 33 ms between updates | 0–0.6% | 1.8% | 2% |
| Panning without updates, with the GPU: frames over 33 ms | 0.3% | 0% | 13–23% |

| 400 services | 1× | 2× | 4× |
|---|---|---|---|
| ELK layout | 1.0–1.7 s | 1.0–1.3 s | 1.2–1.8 s |
| Live update, until painted | 56–78 ms | 140–175 ms | 370–440 ms |

- **Where the time goes:** at 1×, a 200-node update is about 9 ms of script, 10 ms of layout (the text of 200 nodes) and 13 ms of painting.
- **Production versus development:** the production build is about 6× faster than the development server, which first suggested 370 ms per update.
- **Store versus node data:** keeping metrics in `LiveMetrics` instead of React Flow's node data made updates up to a third faster at full speed; under slowdown the results overlapped. Either way, React Flow does no work per update.
- **Labels and containment:** unmeasured labels and containment cut idle-frame jank at 4× from 4–6% to 2%.
- **Bundle:** ELK's worker is 1.42 MB, 433 kB gzipped, and loads only when a layout runs; dagre would be 16 kB. The app's first load is unchanged at 119 kB gzipped, and the canvas chunk is 57 kB.
- **Accessibility:** axe finds no WCAG 2.2 AA violations on the 200-node canvas, in light and dark themes.

Tried and dropped:
- **Spreading one update over several frames** (60 nodes per frame) made slow machines worse, and at 400 nodes updates fell behind.
- **Turning line labels off** saved nothing measurable.
- **ELK's layer wrapping** didn't change the shape of this graph, whose layers are wide rather than many.

Holding updates while panning had mixed results: the headed runs that test it were noisy. It is kept because it costs ten lines and can only remove work during a gesture.

## Consequences

- 200 live services work on ordinary laptops. On a low-end machine an update is a quarter-second hitch every 5 seconds, and panning stutters with or without updates; that is the cost of a DOM-based canvas, which keeps nodes accessible and styleable.
- Groups by team or domain move from a nice-to-have to a requirement of canvas v1 for large projects.
- Metrics reach the canvas through `LiveMetrics`, whatever transport brings them. The stream from the server can send only changed values; the store skips unchanged ones either way.
- If installations need far more than 400 services on one canvas, rendering to a `<canvas>` or WebGL becomes the next step, with an accessible list beside it.

## Alternatives considered

- **Metrics in React Flow's node data.** Measured and rejected: every update makes React Flow process the whole graph, and it was the slowest option under slowdown.
- **dagre instead of ELK.** Smaller and faster at full speed, but it blocks the page under slowdown unless moved into a worker, and it can't lay out nested groups.
- **Rendering to `<canvas>` or WebGL now**, as Cytoscape.js or Sigma.js do. Faster at thousands of nodes, but nodes stop being HTML, so focus, screen readers, text selection and styling must be rebuilt by hand. 200 services don't need it.
