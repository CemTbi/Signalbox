# 0003. Spec engine on libopenapi and vacuum

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Every module reads the OpenAPI document: the designer, mocks, docs, code generation, the client, tests, monitors and drift detection. If the editor, the CLI and the server interpret a spec differently, users lose trust in all of them. The engine must:

- read Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2
- report problems with exact line and column, so the editor can place markers
- separate "invalid document" from "quality or security issue"
- stay fast: lint results for a 10,000-line spec in under one second
- never follow file or remote references for content uploaded to the server

## Decision

`pkg/specengine` is the single implementation, used by the server roles and the CLI. It is a public Go package, so others can reuse it. It has three stages:

1. **Parse** with [libopenapi](https://github.com/pb33f/libopenapi) (MIT). It detects the format and version, and rejects minor versions the engine doesn't know rather than reading them with older rules.
2. **Validate** against the official JSON Schema for the document's version, with [libopenapi-validator](https://github.com/pb33f/libopenapi-validator) (MIT), and resolve every `$ref`. A `$ref` that points nowhere is an error. A circular reference is fine unless every step is required, because then no finite value can satisfy it.
3. **Lint** with [vacuum](https://github.com/daveshanley/vacuum) (MIT), which runs Spectral-compatible rule sets: `recommended`, `owasp` (OWASP API Security Top 10, 2023) and `all`.

Severity policy:

- **Errors** mean the document is invalid or contradicts itself: schema violations, unresolved references, endless reference loops, duplicate operation IDs, undeclared path parameters.
- Lint severities follow vacuum's rule definitions, except where vacuum is stricter than Spectral's defaults. `info-description` and `operation-operationId` are warnings, because a missing description makes a spec incomplete, not invalid.
- Lint results that repeat a problem the validator already reports are dropped, so each problem appears once.

Security: `Options.BaseDir` is empty and `AllowRemoteReferences` is false for anything the server receives. The CLI enables relative file references for local files, and remote references only with `--allow-remote-refs`.

## Spike results (2026-10-16)

| Question | Result |
|---|---|
| Formats | Swagger 2.0 and OpenAPI 3.0.3, 3.1.0, 3.1.1 and 3.2.0 parse and validate. OpenAPI 3.2 features (`query` operations, nested tags, `$self`) validate. |
| Positions | Syntax errors, schema violations, unresolved references and lint results all carry a 1-based line and column. Lint results need positions taken from their YAML nodes, because vacuum fills `Range` only when it builds a report. |
| Speed | A generated 10,017-line spec: full check (parse, validate, lint with `recommended`) in 0.19 s on an AMD Ryzen 5 3600, against a 1 s budget. `TestLargeSpecMeetsBudget` keeps it there. |
| Memory | About 135 MB allocated per check of that spec. Acceptable now; worth profiling before the editor checks on every keystroke. |
| Binary size | `signalbox` and `signalbox-server` are each about 53 MB, stripped. Most of it comes from vacuum's `motor` package depending on `pb33f/doctor`, which brings in a terminal UI and SQLite. |

## Consequences

- The editor, the CLI, CI and the server give identical results for the same document.
- We depend on two projects from one maintainer group (pb33f). Both are active and widely used. We contribute fixes upstream, and the engine's interface hides them, so either can be replaced.
- Watch items: binary size (propose splitting vacuum's `motor` from `doctor` upstream) and allocation volume.
- Browser-side checks stay simple. The editor validates structure instantly with monaco-yaml and the OpenAPI JSON Schema, and calls `POST /api/v1/spec-checks` for full results.

## Alternatives considered

- **kin-openapi with oasdiff.** Rejected. Its OpenAPI 3.1 support is incomplete and it has no 3.2 support.
- **Spectral and Redocly in Node.js.** Rejected. They would put a Node.js runtime in the server and the CLI, and our CLI must be a single static binary.
- **Writing our own parser and validator.** Rejected. It would cost months, and the JSON Schema 2020-12 details are easy to get wrong.
