# Signalbox test action

Runs API test suites in GitHub Actions with [`signalbox test run`](../../docs/adr/0034-cli-test-run.md):
- **offline**, from a Postman collection in your repository, with no Signalbox server;
- **on a server**, from a collection stored on Signalbox, such as [contract tests](../../docs/adr/0035-contract-tests.md) generated from a published spec.

Failed requests and tests show as annotations, the job's summary lists them, and a JUnit report is uploaded as an artifact. The job fails when a request or test fails.

## Offline, from a Postman collection

```yaml
jobs:
  api-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Start the API
        run: docker compose up -d --wait
      - uses: CemTbi/Signalbox/actions/test@v0.2.0
        with:
          collection: tests/shop.postman_collection.json
          environment: tests/ci.postman_environment.json
          env-vars: |
            baseUrl=http://localhost:8080
            apiKey=${{ secrets.SHOP_API_KEY }}
```

Offline requests may reach the runner itself and private networks, such as service containers. Cloud metadata addresses stay refused. Secret variables in the environment file, and values from `secrets`, are shown as `•••` in reports.

## On a Signalbox server

```yaml
      - uses: CemTbi/Signalbox/actions/test@v0.2.0
        with:
          server: https://signalbox.example.com
          token: ${{ secrets.SIGNALBOX_TOKEN }}
          collection-id: col_01j9z3w5y7a9c1e3g5j7m9p1r3
          environment: staging
```

The run happens on the server, with its environments, secrets and egress policy. Its page there has the details. Create the token under **Access tokens** in Signalbox, and keep it in a repository secret.

## Inputs

| Input | Default | What it does |
|---|---|---|
| `collection` | | Offline: a Postman collection file. |
| `collection-id` | | On a server: the collection to run. |
| `server`, `token` | | On a server: its address, and a personal access token. |
| `environment` | | Offline: a Postman environment file. On a server: an environment's ID, name or slug. |
| `env-vars` | | Offline: `key=value` lines that set environment variables. |
| `data` | | A CSV or JSON data file; the requests run once per row. |
| `folder` | | Run one folder: its name, or on a server its ID or name. |
| `iterations` | once per data row, or once | How many times the requests run. |
| `delay-request` | `0` | Milliseconds between requests. |
| `bail` | `false` | Stop at the first failure. |
| `reporters` | `cli,github,junit` | `cli`, `github` (annotations and a job summary), `junit`, `json` and `html`. |
| `out` | `signalbox-reports` | Where report files go. |
| `upload-reports` | `true` | Upload the report files as an artifact. |
| `artifact-name` | `signalbox-reports-<job>` | The artifact's name. Give each job of a matrix its own. |
| `version` | `latest` | The CLI release to install, such as `v0.2.0`. |
| `signalbox-path` | | Use this `signalbox` binary instead of installing one. |

The action installs the CLI from the repository's GitHub releases for the runner's system (Linux, macOS or Windows, x64 or ARM64), after checking the archive against the release's `checksums.txt`. Pin `version` for repeatable runs.

## Outputs

- `exit-code`: 0 when the run passed, 1 when something failed, 2 when the run couldn't start.
