# 0034. `signalbox test run`

- Status: Accepted
- Date: 2026-10-28
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) gates pull requests on test suites. CI runs `signalbox test run` in two ways: offline, from a Postman collection export, with no Signalbox server, like Newman; or against a server, running a stored suite there. [ADR-0033](0033-collection-runs.md) put the run engine and the reports in code that doesn't touch a database, so the CLI can share them.

The CLI runs on a developer's machine or a CI runner, not in a shared server. That changes which destinations it should reach, and how it reports.

## Decision

**Two modes, one command.**
- **Offline:** `signalbox test run collection.json [-e environment.json] [-d data.csv]`. It reads the Postman Collection v2.1 or v2.0 file the way the importer does ([ADR-0031](0031-postman-import.md)), and the Postman environment file the same way. `--env-var key=value` sets or overrides environment variables. It runs the collection, or `--folder <name>`, with the server's engine. What the importer reports as running differently is printed before the run.
- **On a server:** `signalbox test run --server <url> --token <token> --collection col_…`. `--folder` takes a folder's ID or name, and `--environment` takes an environment's ID, name or slug. It starts a run through the API, prints steps as they arrive, cancels the run on Ctrl-C, and downloads the reports the server writes. `SIGNALBOX_SERVER` and `SIGNALBOX_TOKEN` stand in for the flags, so tokens stay out of command lines and CI logs.

Both take `-n/--iterations`, `-d/--data` (CSV or JSON, by extension), `--delay-request <ms>`, `--bail`, `-r/--reporter` (`cli`, `junit`, `json`, `html`, comma-separated, default `cli`) and `-o/--out` (the directory for report files, default the current one). Report files are named after the collection, such as `shop-api.xml`.

**Where offline requests may go.** Offline runs test APIs on the same machine or CI network, which the server's egress policy refuses. So they may reach loopback and private addresses. Link-local addresses, where cloud metadata endpoints live, stay refused, as do multicast and reserved ranges, so a collection from someone else can't read a CI runner's cloud credentials. `--allow <cidr>` and `--deny <cidr>` change this. `--timeout-request <ms>` bounds each request (30 s by default).

**Exit codes**, as for the CLI's other commands: 0 when the run passed, 1 when a request failed, a test failed, or the run was cancelled or interrupted, and 2 when the command couldn't run: bad flags, an unreadable file, or a server that refused.

**Output.** The `cli` reporter prints each request as it ends, with its status, time and failed tests, then the totals. Secrets are masked, as on the server: in an offline run, the environment file's secret variables are.

## Consequences

- A team can gate CI on a Postman export today, without running Signalbox. Once they run a server, the same command runs the stored suite there, and the reports and exit codes stay the same.
- The CLI binary carries the script engine, its bundled libraries and the runner, which makes it about 13 MB larger: 89 to 102 MB for an unstripped Windows build.
- Offline runs differ from server runs only in where requests may go.

## Alternatives considered

- **Refuse private addresses offline, as the server does.** Rejected: testing a service on localhost or a CI service container is the main use. The risk the server guards against, someone else's requests reaching its network, doesn't apply to your own machine. Metadata endpoints stay refused.
- **Server mode through Server-Sent Events.** Deferred, as in ADR-0033: polling works through every proxy.
- **Newman's flag names throughout.** Partly: `-e`, `-d`, `-n`, `--folder`, `--env-var`, `--delay-request`, `--bail`, `--timeout-request` and `-r` match Newman. Report files go to `--out`, named after the collection, instead of a flag per reporter.
