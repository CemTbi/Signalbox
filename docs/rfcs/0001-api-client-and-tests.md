# 0001. The API client, tests and the local agent (v0.2)

- Status: Accepted
- Authors: project lead, Claude
- Discussion: https://github.com/CemTbi/Signalbox/pull/24

## Summary

v0.2 makes Signalbox a place to send requests, keep them, and turn them into tests that gate pull requests:
- an API client with collections, environments, encrypted secrets, a timing breakdown, history, and responses checked against the spec;
- Postman Collection v2.1 import and export, and cURL import, with a report of anything that won't run;
- Postman-compatible scripts;
- test suites run from the web app or by `signalbox test run` in CI, with JUnit, JSON and HTML reports;
- contract tests generated from a published spec version;
- GitHub Actions and GitLab CI templates;
- `signalbox agent`, which runs requests from a laptop so they can reach `localhost` and private networks.

The milestone is done when real Postman collections import with a clean report, and a test suite gates a pull request.

## Motivation

Teams keep their requests in Postman or Insomnia and their tests in Newman scripts. Those copies drift from the spec Signalbox now holds: renamed fields break saved requests silently, and nobody checks that responses still match the published version. v0.1 gave frontend developers a mock to build against; v0.2 gives backend developers and QA the client and tests for the real API, from the same spec, and a way to gate merges on them.

## Proposal

### Modules and roles

| Module | Owns |
|---|---|
| `internal/modules/client` | Collections, folders and requests; collection variables; environment variables and secrets; history; imports and exports. |
| `internal/modules/runner` | Running one request: resolving variables, auth, scripts, sending through the egress dialer ([ADR-0016](../adr/0016-runner-egress.md)), timing, and checking the response against the spec. |
| `internal/modules/testing` | Test suites and runs: iterations, data files, assertions, results and reports. |
| `internal/modules/agents` | Agents: registration, connections, and handing them requests. |
| `internal/modules/identity` (extended) | Personal access tokens, for the CLI and the agent. |

Requests run in the `runner` role ([ADR-0001](../adr/0001-single-binary-with-roles.md)). The API role hands a request to a runner through a small `Jobs` interface:
- **In process** when the same process runs both roles, as `--roles=all` does in Docker Compose. This is the only implementation in v0.2.
- **Over NATS request-reply** when roles run apart. This arrives with the high-availability work, which needs NATS anyway ([ADR-0002](../adr/0002-data-stores.md)). Until then, a deployment that splits the api and runner roles can't send requests, and says so.

### Data model

Collections are stored **one row per folder or request**, not as one document, so two people editing different requests never conflict:

- `collections`: id, project, name, description, collection variables (JSON), pre-request and test scripts, auth, version.
- `collection_items`: id, collection, parent folder (null at the top), `rank` (a fractional index, so items move without renumbering their siblings), kind (`folder` or `request`), name, description, the request (JSON), scripts, and version. Each item has its own ETag.
- **A request** holds the method, URL, query parameters, headers, body (`none`, `raw`, `json`, `urlencoded`, `formdata`; file parts come later), auth, settings (timeout, redirects), and optionally the spec and operation it calls. That link is how responses are checked and how generated requests stay tied to the spec.
- **Auth** is `inherit` (from the folder, then the collection), `none`, `apikey`, `basic`, `bearer`, or `oauth2` with client credentials. Authorization code with PKCE, device code and client certificates come before 1.0.
- `environment_variables`: the project's existing environments (development, staging, production) carry variables, so a request switches between them like Postman environments. Each variable has a key, a value, and whether it's secret.
- `request_history`: each send by a person, with the request as sent (secrets redacted), the status, headers, timings, and up to 1 MiB of the body. It keeps 30 days and at most 1,000 entries per person and project.

**Variables** resolve narrowest first, as in Postman: a script's local values, iteration data, environment, collection, then the built-in `{{$guid}}`, `{{$timestamp}}`, `{{$randomInt}}` family. Workspace-wide globals come later.

### Secrets

Secret variable values are encrypted at rest with AES-256-GCM, under envelope encryption:
- **Key encryption key:** from `SIGNALBOX_SECRET_KEY` or `SIGNALBOX_SECRET_KEY_FILE`, 32 random bytes in base64. An installation without one can't store secrets, and says how to make one.
- **Data keys:** each workspace has one, stored encrypted under the key encryption key. Rotating the key encryption key re-encrypts the data keys only.
- **Write-only:** the API never returns a secret's value after it's written. The web app shows it masked, and only the runner decrypts it, when a request uses it. History and test reports show `•••` in its place.

### Running a request

The runner, for one request:
1. **Pre-request scripts** run, collection's then folder's then request's, in one script session ([ADR-0017](../adr/0017-postman-scripts.md)).
2. **Variables** are resolved, then **auth** is applied.
3. **The request is sent** through the egress dialer, with `net/http/httptrace` timings: DNS, connect, TLS, first byte and download.
4. **Test scripts** run.
5. **Declarative assertions** are checked: status, a header, a value at an RFC 9535 JSONPath, response time, and that the response matches the spec.
6. **The spec check:** if the request is linked to an operation of a published version, the response is checked with libopenapi-validator, the same library mocks use. Violations are reported with their JSON pointers.

The result holds the response (status, headers, at most 10 MiB of body), timings, test and assertion results, script console output, and variables the scripts changed.

### The API client in the web app

- **Layout:** a project's Requests page has a collection tree, a request editor (method, URL with highlighted variables, params, headers, body, auth, scripts, assertions) and a response view (body, headers, timings, tests, spec violations).
- **Choosing where to run:** an environment switcher, and "Run on: Server | My agent".
- **Code snippets** reuse the docs' generators.
- **From a spec:** "New request from operation" fills a request from a published version's operation, as the docs' Try it does.

### Imports and exports

- **Postman Collection v2.1** and its environment files import into a project. The import report lists, per item:
  - what was converted;
  - what will run differently, such as a script that uses something the sandbox lacks, found by parsing scripts for unknown `pm.*` calls and `require`s;
  - what was left out, such as file bodies or unsupported auth types.

  A "clean report" means nothing is left out and nothing will run differently. Collections export back to v2.1.
- **cURL** commands import as single requests.
- **Insomnia and HAR** come before 1.0.

### Tests

- **Test suites:** a suite is a collection or folder run in order, any number of iterations, with a CSV or JSON data file for data-driven runs. Values pass between steps through scripts (`pm.environment.set`) or declarative extractions (a JSONPath into a variable).
- **Runs:** `test_runs` and `test_run_steps` store each run: who or what started it, the environment, the duration and every step's results. Runs start from the web app or the API, and from schedules when monitors arrive in v0.3.
- **`signalbox test run`** works two ways:
  - **Offline**, from files, like Newman: `signalbox test run collection.json -e staging.json -d data.csv`. Nothing touches a Signalbox server, so any CI can gate on a Postman export.
  - **Against a server:** `signalbox test run --server https://signalbox.example.com --token $SIGNALBOX_TOKEN --collection col_… --environment staging` runs a stored suite, server side, and streams its results.

  Both write `--reporter junit,json,html` to `--out`, and exit with 1 when a test fails.
- **Contract tests:** generated from a published spec version. Each operation gets a request filled from its examples, asserting a documented status and a response that matches its schema. The generated collection is linked to the version, and generating it again for a newer version updates it.
- **CI templates:**
  - a GitHub Action (`signalbox-test`) that installs the CLI and runs a suite, with annotations and a job summary;
  - a GitLab CI include, with the JUnit report wired into merge requests.

### The local agent

`signalbox agent` runs on a developer's machine:
- **Connecting:** it signs in with a personal access token, then holds an outbound WebSocket to `/api/v1/agents/connect`. No inbound port is opened.
- **Whose requests it runs:** only its owner's. The web app's "Run on: My agent" sends the request to it; the server never sends anyone else's request to an agent.
- **Network:** the agent uses its own network, so `localhost` and VPN-only hosts work. The egress policy doesn't apply on the agent's side; it's the owner's machine.
- **What travels to it:** secrets reach the agent decrypted, over TLS, only for requests its owner runs.

### Personal access tokens

- **Format:** tokens look like `sbx_pat_…`, are shown once, and are stored as SHA-256 hashes.
- **Lifetime:** each has a name, an expiry (90 days by default, at most a year), and a last-used time.
- **Scope:** a token acts as its person, without the session's CSRF check. Narrower scopes, and service-account tokens (`sbx_sat_…`) for CI that belong to a workspace instead of a person, come before 1.0.
- **Management:** people create and revoke tokens on an Access tokens page.

### API changes

New endpoints, all in `api/openapi.yaml` as they're built:
- **Tokens:** `/api/v1/tokens`.
- **Client:** `/api/v1/projects/{projectId}/collections`, `/api/v1/collections/{collectionId}/items`, `/api/v1/items/{itemId}`, `/api/v1/environments/{environmentId}/variables`, `/api/v1/projects/{projectId}/history`, plus `POST /api/v1/projects/{projectId}/send` (send a request that may not be saved), `.../imports` and `.../export`.
- **Testing:** `/api/v1/collections/{collectionId}/runs`, `/api/v1/runs/{runId}`, and `/api/v1/runs/{runId}/report?format=junit|json|html`.
- **Agents:** `/api/v1/agents` and `/api/v1/agents/connect`.

## Security and privacy

- **Server-side request forgery:** the runner is the main new attack surface. Every request, redirect and `pm.sendRequest` goes through the egress dialer, which refuses Signalbox's own services, metadata endpoints and loopback ([ADR-0016](../adr/0016-runner-egress.md)). Admins can deny more.
- **Scripts:** run in the Sobek sandbox, with no file, process or network access of their own, and time and memory limits ([ADR-0017](../adr/0017-postman-scripts.md)). A script session never outlives one run or crosses users.
- **Secrets:** encrypted at rest and write-only, as above, and redacted from history, logs and reports. Losing the key encryption key loses the secrets; the operations guide says to back it up.
- **Tokens:** hashed and shown once. Every request with one is logged with the token's ID, never its value. Revoking one takes effect on the next request.
- **Agents:** an agent only receives its owner's requests. A connected agent is shown in the owner's settings and can be disconnected there. Tokens used by agents can be revoked.
- **History and bodies:** these may hold personal data from the APIs people call. They're kept 30 days, are visible only to the person who sent them, and are deleted with the project.
- **Imports:** Postman files are parsed with size limits (25 MiB, 10,000 items). Scripts in them never run during import.

## Operations

- **Migrations:** new tables in `client`, `testing`, `agents` and `identity`'s schema, applied at startup as before.
- **New settings:** `SIGNALBOX_SECRET_KEY` and `SIGNALBOX_SECRET_KEY_FILE`; runner limits (timeout, body size, concurrent requests per user); and history retention.
- **Resources:** a runner holds at most `--runner-concurrency` requests at once (default: 8 per CPU), and scripts run one per CPU at a time. A test run's steps run in order, and runs run concurrently up to that limit.
- **Observability:**
  - **Metrics:** requests run, by outcome and where (server or agent); script time; and connected agents.
  - **Logs:** every refused destination is logged with the policy rule.

## Alternatives

- **Store each collection as one JSON document,** like spec revisions. It's simpler to import and export, but every edit to any request conflicts with every other edit to the same collection. Rows per item cost a tree query and fractional ranks.
- **Run requests in the browser.** CORS blocks most APIs, secrets would reach the browser, and the timings and checks would differ from CI's. The agent covers the "my machine's network" case.
- **Newman for CLI runs.** That means a Node.js dependency, and a second implementation of variables, auth and assertions that would drift from the server's. The CLI reuses the runner module.
- **Require NATS from v0.2.** It's more work now, for deployments nobody runs yet. The `Jobs` interface keeps the swap small.

## Unresolved questions

1. **History bodies:** do they belong in object storage from the start? Proposed: PostgreSQL up to 1 MiB per entry for v0.2, and object storage when it arrives for traces.
2. **Service-account tokens for CI:** in v0.2, or before 1.0? Proposed: before 1.0. v0.2's CI path is the offline `test run`, which needs no token.
3. **Arazzo 1.0 import and export:** proposed for after v0.2, on the same suite model.
4. **The order of work,** proposed as these pull requests:
   1. tokens;
   2. the runner and ad-hoc sends with history;
   3. collections, environments and secrets;
   4. Postman and cURL import, and export;
   5. scripts in runs;
   6. test suites, `signalbox test run` and reports;
   7. contract tests;
   8. CI templates;
   9. the agent.
