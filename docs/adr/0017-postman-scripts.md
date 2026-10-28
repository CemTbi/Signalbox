# 0017. Running Postman scripts in Sobek

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

Teams moving to Signalbox bring Postman collections, and most of a collection's value is in its scripts: pre-request scripts that sign requests or fetch tokens, and test scripts that check responses and pass values to the next request. The build plan promises that most Postman scripts run unchanged, with a report of anything that won't.

Postman runs scripts in Node.js with a sandbox of its own. Signalbox's server is one Go binary ([ADR-0001](0001-single-binary-with-roles.md)), so it needs a JavaScript engine in Go. Sobek, the fork of goja that k6 maintains, is the leading one.

Spike 4 had to answer: how many real scripts run, what it costs per script, and how a script is kept from harming the server.

## Decision

`internal/modules/scripts` runs scripts in Sobek, with a sandbox built from **the libraries Postman itself uses**, bundled into one embedded file:

- `pm.request` and `pm.response` are objects from `postman-collection`, and `pm.expect` is chai 4 with Postman's `chai-postman` assertions, so `pm.response.to.have.status(201)` and `pm.request.headers.add(...)` behave as in Postman.
- `pm.test`, `pm.environment`, `pm.collectionVariables`, `pm.globals`, `pm.iterationData`, `pm.variables` (narrowest scope first, with `replaceIn`), `pm.info`, `pm.execution`, `pm.sendRequest` and `setTimeout`.
- The globals of Postman's older sandbox: `tests`, `responseBody`, `responseCode`, `responseTime`, `responseHeaders`, `postman.*`, `xml2Json`, `responseBody.has()`, `_`, `CryptoJS`, `tv4`, `atob` and `btoa`.
- `require` of `ajv`, `buffer`, `chai`, `crypto-js`, `lodash`, `moment`, `postman-collection`, `tv4`, `url`, `uuid` and `xml2js`.

Library versions follow postman-sandbox 6.7.4. The exceptions:
- **Security fixes:** crypto-js, ajv, moment and uuid are raised to releases in the same line that fix known advisories.
- **License:** sax stays at 1.4.1, its last ISC-licensed release.
- **faker:** `postman-collection` only needs it for `{{$random…}}` variables, which Signalbox resolves before a script runs, so it isn't bundled. An npm override puts a stub package in its place, so it isn't installed either.

**How a script runs.** Each library loads the first time a script uses it. A session is one runtime that runs a collection's scripts one after another, as Postman does, so a helper one script stores can be used by the next. A session is never reused across runs or users. A pool keeps a few sessions started and warmed in the background, so a run doesn't wait for the libraries.

**Limits.** A script has no file, process or network access. `pm.sendRequest` goes through the caller, which uses the egress dialer ([ADR-0016](0016-runner-egress.md)). Each script has a time limit, 30 seconds by default, covering the requests and timers it waits for. A watchdog stops a script whose run grows the heap by more than a limit, 256 MB by default. Sobek can't count one runtime's memory, so the watchdog watches the process. It is exact only when one script runs at a time, as in a dedicated runner process.

**Building the bundle.** `internal/modules/scripts/sandbox` holds `pm.js`, the npm manifest and lockfile, and `build.mjs`. The build:
- writes `sandbox.js`, which Go embeds, so Go builds don't need Node.js;
- checks the license of every bundled package against ADR-0008's list;
- writes `THIRD_PARTY_LICENSES.txt`, which is embedded too.

CI rebuilds the bundle and fails if it differs from the committed one.

## Spike results (2026-10-17)

The corpus: 589 public Postman collections from GitHub, holding 3,447 distinct scripts (3,073 test, 374 pre-request). Each script ran with its request. Scripts whose collection saved an example response saw it (106); the others saw a 200 response with an empty JSON object.

| Outcome | Scripts | Share |
|---|---|---|
| Ran to the end (tests may pass or fail) | 2,839 | 82.4% |
| Stopped on the stand-in response's data, such as a missing field; Postman would stop the same way | 548 | 15.9% |
| `ReferenceError` | 52 | 1.5% |
| Script has a syntax error (stray braces; Postman rejects them too) | 4 | 0.1% |
| Waited longer than the 5-second limit used for the test (polling with `setTimeout`) | 4 | 0.1% |
| Used an API or module the sandbox lacks | 0 | 0% |

Most of the `ReferenceError`s are helpers that the collection defines in a collection-level script, which the test ran separately. Only 6 name something Postman's sandbox has and this one doesn't: `cheerio` (5) and the `URL` global (1). The first version of the sandbox also lacked `xml2Json`, `responseBody.has()`, `require('url')`, `require('btoa')` and object bodies in `pm.sendRequest`; the corpus showed each, and they were added.

| Cost | Result |
|---|---|
| Starting a session | 0.5 ms; loading `postman-collection` the first time adds 70 ms, which the pool does in the background |
| One script within a run's session | p50 2 ms, p95 53 ms, p99 167 ms |
| Memory of a warm session | 16 MB |
| Bundle | 1.40 MB of JavaScript from 66 packages, compiled once per process in 0.2 s |
| `while (true) {}` | stopped by the time limit |
| Allocating without end, 64 MB limit | stopped in 0.44 s |

Measured on an AMD Ryzen 5 3600.

## Consequences

- Imported collections keep working: 82% of real scripts ran as they are, and nearly all the rest stopped only because of the stand-in response.
- The import report can name the few unsupported things exactly: `cheerio`, `URL` and modules outside the list above.
- faker's advisories don't reach Signalbox: the stub in `sandbox/faker-stub` is all that is installed or bundled. An update of `postman-collection` keeps the stub, whatever faker version it asks for.
- The memory watchdog is a backstop, not isolation. Scripts from people who don't trust each other must run in runner processes, and on Kubernetes in runner pods with memory limits, as the build plan says.
- Updating a sandbox library means running `npm run build` in its folder. Renovate opens minor and patch updates only, and CI fails until the bundle is rebuilt.
- `{{$random…}}` dynamic variables must be resolved in Go before a script runs.

## Alternatives considered

- **Writing pm and the assertions in Go.** Rejected. chai's language and Postman's assertions are large, and every difference breaks someone's tests. Postman's own libraries behave the same by construction.
- **Running Node.js beside Signalbox.** Rejected. It adds a runtime to every installation and an inter-process protocol, against ADR-0001.
- **Loading every library up front.** Rejected. It costs 80 ms per runtime, against 0.5 ms with lazy loading and a warm pool.
- **A fresh runtime for every script.** Rejected. Postman shares one sandbox across a run, and collections depend on it, for example by storing helper functions for later scripts.
- **goja instead of Sobek.** Sobek is goja with the ECMAScript features k6 needed added, and k6 keeps it maintained.
