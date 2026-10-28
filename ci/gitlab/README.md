# Signalbox tests in GitLab CI

[`signalbox-test.yml`](signalbox-test.yml) is a job template that runs API test suites with [`signalbox test run`](../../docs/adr/0034-cli-test-run.md). Its JUnit report shows failed tests in merge requests, and the reports are kept as artifacts.

Include it, and extend `.signalbox-test`:

```yaml
include:
  - remote: https://raw.githubusercontent.com/CemTbi/Signalbox/v0.2.0/ci/gitlab/signalbox-test.yml

api-tests:
  extends: .signalbox-test
  variables:
    SIGNALBOX_VERSION: v0.2.0
    SIGNALBOX_COLLECTION: tests/shop.postman_collection.json
    SIGNALBOX_ENVIRONMENT: tests/ci.postman_environment.json
    SIGNALBOX_ARGS: --env-var baseUrl=http://api:8080 --bail
  services:
    - name: registry.example.com/shop/api:latest
      alias: api
```

To run a collection stored on a Signalbox server instead, set `SIGNALBOX_COLLECTION_ID` and an environment's name in `SIGNALBOX_ENVIRONMENT`. Set `SIGNALBOX_SERVER` and `SIGNALBOX_TOKEN` as masked CI/CD variables, and the CLI reads them.

## Variables

| Variable | Default | What it does |
|---|---|---|
| `SIGNALBOX_COLLECTION` | | Offline: a Postman collection file. |
| `SIGNALBOX_COLLECTION_ID` | | On a server: the collection to run. |
| `SIGNALBOX_ENVIRONMENT` | | Offline: a Postman environment file. On a server: an environment's ID, name or slug. |
| `SIGNALBOX_DATA` | | A CSV or JSON data file; the requests run once per row. |
| `SIGNALBOX_FOLDER` | | Run one folder. |
| `SIGNALBOX_ITERATIONS` | once per data row, or once | How many times the requests run. |
| `SIGNALBOX_ARGS` | | More flags, such as `--bail`, `--delay-request 100` or `--env-var key=value`. |
| `SIGNALBOX_OUT` | `signalbox-reports` | Where report files go. |
| `SIGNALBOX_VERSION` | `latest` | The CLI release to install. Pin it for repeatable runs. |

The job runs on `alpine:3.22`, on x86-64 or ARM64 runners. It installs the CLI from GitHub releases, after checking the archive against the release's `checksums.txt`.
