# Signalbox

Signalbox is an open-source platform for the whole life of an HTTP API. You design an API, prototype it with a mock, publish its docs, generate code from it, send it requests, test it, watch its health and trace its requests in production. Every module works from the same OpenAPI document, and everything runs on your own infrastructure.

> **Status: pre-alpha.** The project is in Phase 0 of the [roadmap](docs/product/requirements-1.0.md#roadmap). The spec engine, the `signalbox` CLI and the server skeleton work today. Everything else is planned. "Signalbox" is a working title.

## What works today

- **`signalbox lint`** validates Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2 documents against the official JSON Schemas, resolves every `$ref`, and checks the recommended and OWASP API Security rules. Each finding has an exact line and column, and the exit code tells CI whether to fail.
- **`signalbox-server`** runs the `api` role. It serves health endpoints in `application/health+json`, a meta endpoint, and `POST /api/v1/spec-checks`, which checks a document sent over HTTP.

## Try it

You need Go 1.27 or newer. Building the web app also needs Node.js 22 or newer.

```sh
# Check a spec
go run ./cmd/signalbox lint path/to/openapi.yaml

# Gate CI on warnings too, with annotations on the pull request
go run ./cmd/signalbox lint --fail-on warning --format github path/to/openapi.yaml

# Start the server and check a spec over HTTP
go run ./cmd/signalbox-server serve --log-format text
curl -s --data-binary @path/to/openapi.yaml http://localhost:8080/api/v1/spec-checks
curl -s http://localhost:8080/readyz
```

With `make` installed, `make help` lists the common tasks.

## Repository layout

| Path | Contents |
|---|---|
| `cmd/signalbox` | The CLI |
| `cmd/signalbox-server` | The server; `--roles` picks which roles a process runs |
| `pkg/specengine` | Parsing, validation and linting of OpenAPI documents, shared by the CLI and the server |
| `internal/` | Server internals: roles, HTTP API, health checks, embedded web app |
| `api/openapi.yaml` | Signalbox's own REST API, the contract the server and web app follow |
| `web/` | The React web app |
| `deploy/` | Docker image and Docker Compose files |
| `docs/` | Architecture decisions, RFCs and product requirements |

## Documentation

- [Product requirements for 1.0](docs/product/requirements-1.0.md): every module's scope, milestones and targets
- [Architecture decision records](docs/adr/README.md): why the system is built the way it is
- [RFCs](docs/rfcs/README.md): how to propose a significant change
- [Repository setup](docs/maintainers/repository-setup.md): the GitHub settings and apps the project relies on

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) first. Every commit needs a [DCO](https://developercertificate.org/) sign-off (`git commit -s`). Questions go to [SUPPORT.md](SUPPORT.md); security reports follow [SECURITY.md](SECURITY.md).

## License

Signalbox is licensed under the [Apache License 2.0](LICENSE). Documentation is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
