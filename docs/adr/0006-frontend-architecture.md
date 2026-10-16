# 0006. Frontend architecture

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Most of Signalbox's users meet it in the browser: a code editor for specs, a request client, a node graph for the canvas, and trace waterfalls with thousands of spans. Installations can be air-gapped, so the app can't load anything from the internet. It must be accessible, fast on first load, and easy for contributors to work on.

## Decision

| Concern | Choice |
|---|---|
| Language and build | TypeScript in strict mode, React, Vite |
| Workspace | npm workspaces: `web/app` plus shared packages in `web/packages/*` (`ui`, `api-client`, later `docs-renderer`) |
| Routing and server state | TanStack Router and TanStack Query |
| UI primitives | Radix UI for behavior and accessibility, styled with our own design tokens |
| Spec editor | Monaco with monaco-yaml, fed the OpenAPI JSON Schemas for instant structural feedback |
| Canvas | React Flow with ELK.js for automatic layout |
| Charts and trace waterfall | uPlot for time series; a custom canvas renderer for waterfalls with 10,000+ spans |
| API types | Generated from `api/openapi.yaml` with openapi-typescript; requests through openapi-fetch |
| Tests | Vitest and Testing Library for components, Playwright for end-to-end journeys, axe-core for accessibility |

Rules:

- **Served by the api role.** `npm run build` writes the app into `internal/webui/dist`, and `go:embed` bundles it into `signalbox-server`. One binary serves the API and the app from the same origin, so there is no CORS to configure.
- **Nothing from the internet.** Fonts, icons and editor workers are bundled. The Content Security Policy allows only the app's own origin.
- **Accessibility target:** WCAG 2.2 AA, checked by axe-core in CI and by keyboard and screen-reader review.
- **Performance budget:** at most 350 kB of gzipped JavaScript before the editor loads. Monaco, the canvas and the waterfall load on demand.
- **Text is translation-ready from the start.** English ships first.

npm is the package manager. It ships with Node.js, and Node.js 25 no longer bundles corepack, so pnpm would be one more prerequisite for contributors.

TypeScript stays on 5.9 for now. TypeScript 7 is the new native compiler, and the tools that read TypeScript's JavaScript API haven't caught up: openapi-typescript requires 5.x and typescript-eslint supports up to 6.0. Renovate holds TypeScript below 6 until both support newer versions.

## Consequences

- Contributors need only Node.js to work on the app, and only Go to work on the server.
- Same-origin serving keeps session cookies `SameSite=Lax` and avoids CORS preflights.
- Monaco needs `worker-src blob:` in the Content Security Policy once the editor lands.

## Alternatives considered

- **Next.js or another server-rendered framework.** Rejected for the app. It is an authenticated tool, not a public site, and server rendering would add a Node.js runtime to the deployment. Published docs may still be prerendered later.
- **A separately deployed frontend.** Rejected. It adds a deployment and cross-origin configuration for no benefit.
- **pnpm.** A good tool, but not worth an extra prerequisite yet. Easy to revisit.
