# Changelog

All notable changes to Signalbox are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/). Before 1.0, minor versions may contain breaking changes, always with migration notes.

## [Unreleased]

### Added

- Spec engine (`pkg/specengine`): parses Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2; validates against the official JSON Schema for each version; resolves `$ref`s and detects circular references that can never end; lints with the recommended and OWASP API Security rule sets. Every diagnostic carries a line and column.
- `signalbox lint` with text, JSON and GitHub Actions annotation output, `--fail-on` and `--min-severity` thresholds, and standard input support.
- `signalbox-server` with the `api` role: `/livez` and `/readyz` in `application/health+json`, `GET /api/v1/meta`, `POST /api/v1/spec-checks`, RFC 9457 error responses, request IDs, security headers, a separate ops listener and graceful shutdown with a drain delay.
- The platform's own API contract in `api/openapi.yaml`.
- Project governance, contribution guide, security policy, and the first eight architecture decision records.
