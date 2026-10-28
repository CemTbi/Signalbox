# 0033. Running collections as test suites

- Status: Accepted
- Date: 2026-10-28
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) makes a collection, or a folder of one, a test suite: its requests run in order, any number of times, optionally once per row of a CSV or JSON data file, and the run is kept with every step's results and reported as JUnit, JSON or HTML. `signalbox test run` runs the same suites in CI, offline from Postman files or on a server.

One request already runs with its scripts ([ADR-0032](0032-scripts-in-sends.md)). A run is many of those, with variables carried from one to the next. The server's run and the CLI's offline run must behave the same, or a suite that passes in CI fails in the web app.

## Decision

**One engine.** The client module runs a *plan*: a collection's requests in order, each with its script chain (collection, folders, request), its saved request and its auth. The engine resolves variables, runs scripts, applies auth, sends through the runner and masks secrets. It doesn't touch the database. Sending one request is a plan of one step, so the Requests page, a server run and the CLI share one code path. A new module, `internal/modules/testruns`, owns runs: storing them, queueing and cancelling them, data files and reports. The RFC called it `testing`. That name collides with Go's `testing` package in every test file, so the module is called `testruns`.

**What a run runs.** A collection, or one folder of it, depth first in the order the sidebar shows. Every request runs. Folders contribute their scripts. A run takes:
- an environment (optional);
- `iterations`, 1 to 1,000. With a data file and no count, there's one iteration per row. With both, iterations past the last row reuse the last row, as Newman does;
- a data file: CSV with a header row, or a JSON array of objects. At most 1,000 rows and 5 MiB. Its row fills `pm.iterationData` and `{{name}}`;
- `delayMs` between requests, 0 to 60,000;
- `bail`: stop at the first request that fails or has a failing test.

A run has at most 10,000 steps (requests × iterations) and lasts at most an hour.

**Variables.** They resolve narrowest first, as for a send: what scripts set locally, the iteration's data, the environment, the collection. What a script sets in the environment or the collection scope carries to the next request and the next iteration, as Newman does. Local values last for one request. Nothing is saved to the environment or the collection ([ADR-0032](0032-scripts-in-sends.md)).

**Scripts.** A run uses one script session, as ADR-0017 intends: helpers a collection script stores are there for every request. The session never outlives the run. Each step takes one of the four script slots that sends use while its scripts run, so at most four sets of scripts run at once in a process, whatever mix of sends and runs. `pm.info` carries the iteration, iteration count and request name. `pm.execution.setNextRequest(name)` jumps to the first request with that name, or its ID, in the run. `null` ends the iteration, and so does a name the run doesn't have, with a note on the step. The 10,000-step limit also stops loops. The sandbox sends `null` to Go as `""`, which no request can be named.

**Where runs run.** In the process with the runner role, like sends. A process runs at most two runs at once; more wait as `queued`. A run's status is `queued`, `running`, `passed`, `failed`, `canceled` or `interrupted`. `passed` means every request got a response, every test passed and no script failed. A skipped request (`pm.execution.skipRequest()`) doesn't fail a run. When the server stops, runs in progress are cancelled and become `interrupted`. A process that died can't say so, so each run keeps a heartbeat in its row while its process is alive. At startup, and every minute, a run whose heartbeat is more than two minutes old becomes `interrupted`. Another live process's runs aren't touched. Cancelling sets a flag in the run's row, which the run checks after each step, so it works whichever process received the request.

**Who.** Starting and cancelling a run sends requests, so owners, admins and editors may, as for a send. Every member of the project may read runs and reports. Runs are the project's shared test results, unlike history, which is personal. Like history, they hold masked values only.

**What's kept.** For a run: who started it, the collection or folder and its name then, the environment, the options, the totals (requests, failed requests, tests, failed tests), the duration and why it stopped. For each step: the iteration, the request's ID, name and folder path, the method and URL as sent, status, duration, failure, tests, script errors, console output, variable changes and unresolved names. Secrets are masked as in history. A step's response body is kept, first 16 KiB and masked, only when the step failed or a test failed, since that's when someone needs it. Runs are deleted with their collection, after 90 days, and beyond the newest 100 of each collection.

**API.**
- `POST /api/v1/collections/{collectionId}/runs` starts a run and answers 202 with it.
- `GET /api/v1/collections/{collectionId}/runs` lists runs, newest first.
- `GET /api/v1/runs/{runId}` returns a run with its totals.
- `GET /api/v1/runs/{runId}/steps?after=&limit=` lists step summaries in order, for following a run as it goes.
- `GET /api/v1/runs/{runId}/steps/{seq}` returns one step in full.
- `POST /api/v1/runs/{runId}/cancel` cancels a run.
- `GET /api/v1/runs/{runId}/report?format=junit|json|html` downloads a report.

**Reports** are built from a stored run by code that doesn't touch the database, so the CLI writes the same files:
- **JUnit XML:** a `testsuite` per request per iteration, and a `testcase` per test. A request that failed, or a script error, is an `error`. A failed test is a `failure`.
- **JSON:** the run and every step, in the API's shapes.
- **HTML:** one self-contained page, with no scripts or external files, so it opens from a CI artifact.

**Web app.** Collections and folders get a Run action, which opens a dialog for the options above. The run's page follows it as it goes, lists steps with their tests, opens a step's details, and downloads the reports. A collection's past runs are listed beside it.

## Consequences

- A collection that passes in Postman's runner or Newman should pass here. Differences are the documented sandbox gaps (ADR-0017), and a login token that isn't saved between runs.
- The send path moved into the engine. The Requests page behaves as before, and its tests still cover it.
- Runs hold the runner for up to an hour. Two at once per process, with steps sharing the script slots, keeps sends responsive.
- `signalbox test run` reuses the engine and the reports. Its offline mode, and the server mode that starts and follows a run through this API, come in the next change.

## Alternatives considered

- **Runs in the client module.** Rejected: the client module already owns collections, variables, history and imports. Runs, their queue and reports are a separate job with their own tables.
- **Synchronous runs, answering when done.** Rejected: runs take minutes, which proxies and browsers time out, and the web app wants progress.
- **Streaming steps over Server-Sent Events.** Deferred: polling steps after the last one seen is simple, works through every proxy, and serves the CLI too.
- **Keeping every response body.** Rejected: 10,000 steps of bodies is a lot of storage, mostly for requests that passed.
- **A new script session per request.** Rejected: collections rely on helpers their collection script stores, which Postman keeps for a whole run.
