# 0005. HTTP API conventions

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

The web app uses only the public REST API, and so do the CLI, CI integrations and third-party tools. An API platform's own API has to be exemplary: consistent, documented and checked by its own tools.

## Decision

**The contract comes first.** `api/openapi.yaml` (OpenAPI 3.1) is the source of truth. Every API change edits it in the same pull request as the code. CI runs `signalbox lint` on it and reports breaking changes. Integration tests validate every response against it with libopenapi-validator. The web app's TypeScript types are generated from it with openapi-typescript.

Server stubs aren't generated yet. oapi-codegen 2.8.0 doesn't read OpenAPI 3.1, and our contract tests catch drift between handlers and the spec. A spike will compare ogen and libopenapi's generator before the API grows past the walking skeleton.

**Conventions**

| Topic | Rule |
|---|---|
| Base path | `/api/v1`. Breaking changes only go into a new major path. |
| Format | JSON with camelCase field names. Absent optional fields are omitted, not `null`. |
| IDs | Opaque strings with a type prefix, such as `ws_`, `prj_`, `svc_` or `spec_`, followed by a sortable 26-character identifier. |
| Time | RFC 3339 in UTC, such as `2026-10-16T12:00:00Z`. |
| Collections | `GET /parents/{id}/children` lists and `POST` creates. Items have flat paths such as `/projects/{projectId}`, because IDs are unique across the installation. |
| Pagination | Cursor-based: `limit` (1 to 200, default 50) and `cursor` parameters; responses are `{"data": [...], "nextCursor": "..."}`. |
| Errors | RFC 9457 problem details (`application/problem+json`) with a stable `code`, the `requestId`, and an `errors` array for field validation problems. |
| Concurrency | `ETag` on resources people edit. Updates send `If-Match` and get `412 Precondition Failed` on a conflict. Spec revisions require `If-Match`, otherwise `428 Precondition Required`. |
| Idempotency | `POST` endpoints that create resources or start runs accept `Idempotency-Key`; repeats within 24 hours return the first result. |
| Rate limits | `429 Too Many Requests` with `Retry-After` and the IETF `RateLimit` headers. |
| Deprecation | `Deprecation` (RFC 9745) and `Sunset` (RFC 8594) headers, at least six months before removal. |
| Request IDs | Every response has `X-Request-Id`; a valid incoming one is reused. |
| Webhooks | Signed according to the Standard Webhooks specification. |
| Health | `/livez` and `/readyz` outside `/api`, in `application/health+json`. |

## Consequences

- Clients can rely on one error shape, one pagination style and one concurrency model everywhere.
- Signalbox dogfoods its own engine on its own contract from the first commit.
- Without generated stubs, handlers are written by hand. The contract tests carry the weight until generation is settled.

## Alternatives considered

- **gRPC or Connect as the primary API.** Rejected. Browsers, curl and the API tools Signalbox replaces all speak HTTP and JSON. gRPC may still be used between roles.
- **GraphQL.** Rejected for the public API. Caching, rate limiting and per-endpoint authorization are simpler with REST, and Signalbox's own docs module renders REST best.
- **Writing the API in OpenAPI 3.0 for generator support.** Rejected. 3.1's full JSON Schema support matters for an API product, and the generator gap is closing.
