# Signalbox

Signalbox is an open-source platform for the whole life of an HTTP API. You design an API, prototype it with a mock, publish its docs, generate code from it, send it requests, test it, watch its health and trace its requests in production. Every module works from the same OpenAPI document, and everything runs on your own infrastructure.

> **Status: pre-alpha.** The project is building the walking skeleton from the [roadmap](docs/product/requirements-1.0.md#roadmap). Sign-in, workspaces, projects, an empty canvas and the spec engine work today; everything else is planned.

## What works today

- **`signalbox lint`** validates Swagger 2.0 and OpenAPI 3.0, 3.1 and 3.2 documents against the official JSON Schemas, resolves every `$ref`, and checks the recommended and OWASP API Security rules. Each finding has an exact line and column, and the exit code tells CI whether to fail.
- **`signalbox diff`** compares two versions of an API and reports which changes break clients, such as a removed operation or a parameter that became required, with the operation and line of each.
- **`signalbox codegen`** generates a Go client or a Go server stub on `net/http` from a document, and warns about the OpenAPI 3.1 features the code can't express.
- **`signalbox-server`** stores its data in PostgreSQL and migrates the schema at startup. A new installation asks for a setup code from the server log and creates the first admin account. People sign in with a password or through their organization's OpenID Connect provider; workspaces have owners, admins, editors and viewers, who bring others in with invitation links; each project gets development, staging and production environments, a canvas, and services whose OpenAPI specs are checked, kept in revisions, published as versions and served as hosted mocks. Spec checks run in the browser and over the API.
- **The REST API** in [`api/openapi.yaml`](api/openapi.yaml) covers everything the web app does. Errors are RFC 9457 problem documents, edits use ETags, and lists page with cursors.

## Try it

You need Docker. From a clone of this repository:

```sh
docker compose -f deploy/compose/compose.yaml up -d --build
docker compose -f deploy/compose/compose.yaml logs signalbox | grep setup_code
```

Open http://localhost:8080, enter the setup code from the log and create your admin account. Then create a workspace and a project. To serve Signalbox at another address, set `SIGNALBOX_PUBLIC_URL`; to turn on single sign-on, set the `SIGNALBOX_OIDC_` settings. The comments in [`compose.yaml`](deploy/compose/compose.yaml) explain them.

To check a spec from the command line, you need Go 1.27 or newer:

```sh
go run ./cmd/signalbox lint path/to/openapi.yaml

# Gate CI on warnings too, with annotations on the pull request
go run ./cmd/signalbox lint --fail-on warning --format github path/to/openapi.yaml

# Fail on breaking changes against the main branch
git show origin/main:path/to/openapi.yaml | go run ./cmd/signalbox diff --format github - path/to/openapi.yaml

# A Go client in ./orders
go run ./cmd/signalbox codegen --target go-client path/to/openapi.yaml
```

To work on Signalbox itself, see [CONTRIBUTING.md](CONTRIBUTING.md). With `make` installed, `make help` lists the common tasks.

## Repository layout

| Path | Contents |
|---|---|
| `cmd/signalbox` | The CLI |
| `cmd/signalbox-server` | The server; `--roles` picks which roles a process runs |
| `pkg/specengine` | Parsing, validation and linting of OpenAPI documents, shared by the CLI and the server |
| `internal/modules/` | The product's modules, such as identity and workspaces, each with its own tables |
| `internal/` | Server internals: roles, HTTP API, health checks, database access, embedded web app |
| `api/openapi.yaml` | Signalbox's own REST API, the contract the server and web app follow |
| `web/` | The React web app |
| `e2e/` | End-to-end tests that drive Signalbox in a browser |
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
