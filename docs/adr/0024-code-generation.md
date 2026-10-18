# 0024. Code generation, starting with Go

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

v0.1 promises code generation. The requirements for 1.0 ask for:
- server stubs for Go, Java Spring, Python FastAPI and ASP.NET Core;
- client SDKs for TypeScript, Python, Go, Java and C#;
- output pinned to a spec version, as a zip download or from `signalbox codegen`;
- a warning whenever a 3.1 or 3.2 feature can't be expressed by the chosen generator.

The build plan chose oapi-codegen in process for Go, and openapi-generator in an optional container for the other languages, with documents down-converted to OpenAPI 3.0 for the generators.

## Decision

**`pkg/codegen` generates Go clients and server stubs with oapi-codegen** (Apache-2.0), in process.
- **Client** (`go-client`): a type per schema and a client with a method per operation. It has a variant that decodes each response into its status code's type.
- **Server** (`go-server`): a strict server on `net/http`. Each operation is a method that takes a typed request and returns a typed response, with a router, and the spec is embedded for request-checking middleware.

Each target writes `<package>/<package>.gen.go` and a README. The package name comes from the document's title unless one is given.

**Documents are read with kin-openapi through `pkg/specload`**, which `pkg/specdiff` now shares ([ADR-0023](0023-breaking-changes.md)): Swagger 2.0 is converted, and references follow the same rules as everywhere else.

**OpenAPI 3.1 is prepared rather than down-converted wholesale.** oapi-codegen reads most of 3.1 through kin-openapi; for example, `type: [string, "null"]` becomes a pointer. So instead of converting every document to 3.0, a preparation step changes only what breaks or misleads the generator, on the YAML tree before loading:
- **Several types**, such as `type: [string, integer]`, which oapi-codegen refuses, become a `oneOf` with one schema per type. A `null` among them becomes `nullable`.
- **`const`**, which would become an untyped value, becomes an `enum` with one value.

**What generators can't express is reported**, with the line and column in the document:
- `prefixItems`, `if`/`then`/`else`, `patternProperties`, `dependentSchemas`, `dependentRequired` and `$dynamicRef`;
- OpenAPI 3.2's `query` method and `additionalOperations`.

`signalbox codegen --fail-on-warning` turns these into a failure for CI.

**`signalbox codegen --target <target> <file>`** writes the package under `--out`. Generated `.gen.go` files are replaced on every run; a README someone has changed is kept.

**The TypeScript client (`typescript-client`) is Signalbox's own generator, in Go.** It's what frontend developers building against a mock need first, and it shouldn't need a JVM or Node.js where it runs.
- **Output:** one `index.ts` with no dependencies, on `fetch`. It has a type per component schema, and a client whose methods each take one object of parameters and `body`. A method resolves to its success response's body; other statuses throw `ApiError` with the parsed body, such as an RFC 9457 problem.
- **Types:** 3.1's type lists, `const` and nullable types map to TypeScript unions and literal types directly.
- **Parameters:** cookie parameters are left out, because browsers don't let scripts set cookies.
- **Stability:** output is sorted, so the same document always gives the same file.
- **Testing:** the tests type-check clients with `tsc --strict`, including one for Signalbox's own API, and run one against a fake `fetch`.

Python, Java and C# come through openapi-generator, in a container an installation opts into, as the plan says.

## Consequences

- Teams can generate Go clients and servers and TypeScript clients from the CLI today, and in CI.
- Generated Go code needs `github.com/oapi-codegen/runtime`, which the README says how to add. Signalbox's tests compile the generated code against it.
- The CLI binary grows by about 5 MB, from 61 to 66 MB.
- The preparation step rewrites only the document given to it. A schema with several types in a file it references still fails to generate, with oapi-codegen's error.
- Zip downloads from published versions, and a target list in the web app, come with the server endpoint.

## Alternatives considered

- **Down-convert every document to OpenAPI 3.0 first.** Rejected for Go: oapi-codegen already reads 3.1, and converting everything would lose more than the two rewrites above. Containers running openapi-generator may still need it.
- **openapi-generator for Go too.** Rejected: it needs a JVM, and oapi-codegen's Go is idiomatic and widely used.
- **Write our own Go generator.** Rejected: oapi-codegen covers what we need, and is maintained.
