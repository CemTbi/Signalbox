# Changelog

All notable changes to Signalbox are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/). Before 1.0, minor versions may contain breaking changes, always with migration notes.

## [Unreleased]

### Added

- Spec engine (`pkg/specengine`): parses Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2; validates against the official JSON Schema for each version; resolves `$ref`s and detects circular references that can never end; lints with the recommended and OWASP API Security rule sets. Every diagnostic carries a line and column.
- `signalbox lint` with text, JSON and GitHub Actions annotation output, `--fail-on` and `--min-severity` thresholds, and standard input support.
- `signalbox-server` with the `api` role: `/livez` and `/readyz` in `application/health+json`, `GET /api/v1/meta`, `POST /api/v1/spec-checks`, RFC 9457 error responses, request IDs, security headers, a separate ops listener and graceful shutdown with a drain delay.
- The platform's own API contract in `api/openapi.yaml`.
- Project governance, contribution guide, security policy, and the first eight architecture decision records.
- PostgreSQL storage. The server waits for the database, applies migrations at startup under an advisory lock, and reports each stage through `/readyz`. `signalbox-server migrate` runs migrations as a separate step.
- First-run setup: a new installation logs a setup code, and the setup page uses it to create the first admin account. `SIGNALBOX_SETUP_CODE` sets the code instead.
- Sign-in with email and password: Argon2id hashes, server-side sessions that end after 12 hours idle or 7 days, CSRF protection through the request origin and a per-session token, and rate limits per client address and per account.
- Workspaces with owner, admin, editor and viewer roles, and projects with development, staging and production environments, in the API and the web app. Edits use ETags and lists page with cursors.
- The web app now has sign-in, setup, workspace and project pages, an empty canvas for each project, and the spec check at `/spec-check`.
- New settings: `SIGNALBOX_DATABASE_URL`, `SIGNALBOX_PUBLIC_URL`, `SIGNALBOX_TRUSTED_PROXIES`, `SIGNALBOX_SETUP_CODE`, `SIGNALBOX_MIGRATE` and `SIGNALBOX_DATABASE_WAIT`.

### Changed

- `POST /api/v1/spec-checks` now needs a signed-in session.
- The server now needs PostgreSQL. `make dev-db` starts one for development.
