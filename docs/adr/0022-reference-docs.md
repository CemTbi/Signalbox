# 0022. Reference docs of published versions

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

v0.1 promises published docs. A frontend developer building against a mock ([ADR-0021](0021-hosted-mocks.md)) needs to see what each operation takes and returns, without reading YAML. The requirements for 1.0 ask for:
- docs generated on publish, for every published version;
- operations grouped by tag, including OpenAPI 3.2's nested tags;
- code samples in curl, JavaScript, Python, Go, Java and C#;
- a Try it console, a version switcher, internal or public visibility, a Swagger UI view and static HTML export.

## Decision

**The server builds a reference model; the web app renders it.** `pkg/refdocs` turns an OpenAPI 3 document into a `Reference`:
- operations grouped by tag in the document's order, with untagged ones last;
- parameters, with the path item's merged into each operation's;
- request bodies and responses, with response codes in numeric order;
- schemas with references to components resolved in place;
- the security each operation needs.

`GET /api/v1/versions/{versionId}/reference` serves it. A version never changes, so the reference is built once per revision and cached, like mocks. Its revision is its ETag, and `If-None-Match` gets `304`. The same model can be rendered as static HTML later, and the CLI can build it locally. Parsing OpenAPI once, in Go, keeps docs, mocks and checks reading documents the same way.

**Schemas are expanded, within bounds.** A reference to a component is expanded where it's used, so readers see a body's fields without jumping. A reference to a component already being expanded, or more than three references deep, becomes a link to the component's section. A reference expands at most 50,000 schemas in total. Without these limits, a schema that refers to another twice at every level would expand exponentially, as libopenapi's own expansion does ([ADR-0021](0021-hosted-mocks.md)).

**Examples are the mock's.** Each body shows the document's named examples, or one built from its example or schema with the mock's generator and seed. So what the docs show is what the mock sends. A body too large to generate shows no example.

**Descriptions are CommonMark, rendered in the browser by micromark** with the GitHub tables extension. Anyone who can edit a spec writes these descriptions, and docs pages run on Signalbox's origin with the reader's session. So raw HTML is escaped and links with protocols such as `javascript:` are dropped; both are micromark's defaults. markdown-it was considered, but it ships `argparse` under the Python-2.0 license, which isn't on the allowlist ([ADR-0008](0008-dependency-policy.md)).

**Code samples are written in the browser** from the model, for the base URL the reader picks: the version's mock or one of the document's servers. They include required parameters, the first example body, and placeholders such as `$TOKEN` for the first way to authorize.

**Try it sends requests to the mock only, for now.** The mock is on the same origin and allows any origin, so the browser can call it directly. Requests to staging or production need the runner, which keeps credentials off the page and enforces egress rules ([ADR-0016](0016-runner-egress.md)). Try it will use the runner when it exists.

**Docs are internal for now.** Members of the project can read them. Public docs, the Swagger UI view, static export and Swagger 2.0 (which answers `501 reference-unsupported`) come later.

## Consequences

- Every published version has docs at `/specs/{specId}/docs`, with `?version=` for older ones, as soon as it's published.
- The reference is a contract other renderers can use; changes to it go through `api/openapi.yaml`.
- Very large or deeply shared schemas show links to components instead of fully expanded fields.
- Webhooks and callbacks aren't shown yet.

## Alternatives considered

- **Render the YAML in the browser with an existing component**, such as Redoc or Scalar. Rejected. They bring their own parser, which would disagree with the spec engine about edge cases, and large bundles. A themed view of our own model fits the app, and Swagger UI stays an option for people who want it.
- **Generate static HTML on publish.** Deferred. Serving the model keeps one rendering path for the app; static export can render the same model later.
- **Expand every reference fully.** Rejected: exponential for shared schemas.
