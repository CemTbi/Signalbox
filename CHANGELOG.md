# Changelog

All notable changes to Signalbox are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/). Before 1.0, minor versions may contain breaking changes, always with migration notes.

## [Unreleased]

### Added

- Spec engine (`pkg/specengine`): parses Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2; validates against the official JSON Schema for each version; resolves `$ref`s and detects circular references that can never end; lints with the recommended and OWASP API Security rule sets. Every diagnostic carries a line and column.
- Published versions offer generated code as a zip, from the spec page or `GET /api/v1/versions/{versionId}/code?target=…`: a TypeScript client, a Go client or a Go server stub, with `WARNINGS.md` when the code can't express part of the spec. The same version always gives the same archive.
- `signalbox codegen --target typescript-client <file>` generates a TypeScript client with no dependencies, on `fetch`: a type per schema, and a method per operation that takes its parameters and body in one object, resolves to the success response's body, and throws `ApiError` for other statuses.
- `signalbox codegen --target go-client|go-server <file>` generates a Go client, or a Go server stub with typed requests and responses on `net/http`, with oapi-codegen. Several types in one schema and `const` are rewritten so the generator can express them, and the OpenAPI 3.1 and 3.2 features it can't express are reported with their lines; `--fail-on-warning` fails CI on them.
- `signalbox diff <base> <head>` reports what changed between two versions of an API: breaking changes as errors, changes that may break some clients as warnings, and the rest as information, each with its operation and line. Output is text, JSON, GitHub Actions annotations or a Markdown table, and the exit code fails CI on breaking changes. Signalbox's own CI reports changes to its API on every pull request.
- `signalbox lint` with text, JSON and GitHub Actions annotation output, `--fail-on` and `--min-severity` thresholds, and standard input support.
- `signalbox-server` with the `api` role: `/livez` and `/readyz` in `application/health+json`, `GET /api/v1/meta`, `POST /api/v1/spec-checks`, RFC 9457 error responses, request IDs, security headers, a separate ops listener and graceful shutdown with a drain delay.
- The platform's own API contract in `api/openapi.yaml`.
- Project governance, contribution guide, security policy, and the first eight architecture decision records.
- PostgreSQL storage. The server waits for the database, applies migrations at startup under an advisory lock, and reports each stage through `/readyz`. `signalbox-server migrate` runs migrations as a separate step.
- First-run setup: a new installation logs a setup code, and the setup page uses it to create the first admin account. `SIGNALBOX_SETUP_CODE` sets the code instead.
- Sign-in with email and password: Argon2id hashes, server-side sessions that end after 12 hours idle or 7 days, CSRF protection through the request origin and a per-session token, and rate limits per client address and per account.
- Workspaces with owner, admin, editor and viewer roles, and projects with development, staging and production environments, in the API and the web app. Edits use ETags and lists page with cursors.
- The web app now has sign-in, setup, workspace and project pages, an empty canvas for each project, and the spec check at `/spec-check`.
- Workspace members and invitations. Owners and admins create an invitation link for an email address and a role, and send it however they like; the invitee creates an account from it, or signs in and accepts. Members' roles can change, people can be removed or leave, and a workspace always keeps an owner.
- Single sign-on through an OpenID Connect provider, such as Okta, Microsoft Entra ID, Google, Keycloak or Dex. Someone signing in for the first time gets an account if the provider has verified their email address and no account has it; existing accounts are never taken over by email address. `SIGNALBOX_OIDC_ALLOWED_DOMAINS` limits which email domains get accounts. Invitation links work with single sign-on too.
- Reference docs: every published version has docs at `/specs/{specId}/docs`, with a version switcher. Operations are grouped by tag, with their parameters, bodies, responses and schemas, and the examples the mock sends. Each has code samples in cURL, JavaScript, Python, Go, Java and C#, for the mock or any of the API's servers, and a Try it form that calls the mock. `GET /api/v1/versions/{versionId}/reference` serves the same reference.
- Hosted mocks: every published version answers at `/mocks/{specId}/{version}`, or `/latest`, without sign-in and from any origin. Responses come from the spec's examples or are generated from its schemas, the same for the same request; requests are checked, and invalid ones get a problem naming the field. `Prefer: code=404` and `Prefer: example=name` choose the response. The spec page shows each version's mock address. The work a version's mock may take is bounded, so schemas that refer to one another many times can't exhaust the server.
- Published versions: publish a spec's revision under a name such as 1.4.0, with release notes, from the API or the spec's page. A revision with errors can't be published. Mocks, docs and code generation will serve published versions.
- Services and their specs in the web app: add services to a project, add a spec by pasting or uploading it, see its problems by line, edit it and save revisions with a note, and read its history. Viewers see everything read-only.
- Services and their OpenAPI specs in the API. Every save of a spec is a numbered revision that never changes, checked with the recommended rules, with its report, content and SHA-256. Saves need the spec's current ETag, so two people can't overwrite each other's work. Spec check reports now include the document's title and API version.
- New settings: `SIGNALBOX_DATABASE_URL`, `SIGNALBOX_PUBLIC_URL`, `SIGNALBOX_TRUSTED_PROXIES`, `SIGNALBOX_SETUP_CODE`, `SIGNALBOX_MIGRATE` and `SIGNALBOX_DATABASE_WAIT`; for single sign-on, `SIGNALBOX_OIDC_ISSUER`, `SIGNALBOX_OIDC_CLIENT_ID`, `SIGNALBOX_OIDC_CLIENT_SECRET` (or `SIGNALBOX_OIDC_CLIENT_SECRET_FILE`), `SIGNALBOX_OIDC_NAME`, `SIGNALBOX_OIDC_SCOPES`, `SIGNALBOX_OIDC_ALLOWED_DOMAINS` and `SIGNALBOX_OIDC_TRUST_UNVERIFIED_EMAIL`.

### Changed

- `POST /api/v1/spec-checks` now needs a signed-in session.
- The server now needs PostgreSQL. `make dev-db` starts one for development.
