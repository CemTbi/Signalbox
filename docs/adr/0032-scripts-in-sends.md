# 0032. Running scripts when a request is sent

- Status: Accepted
- Date: 2026-10-20
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) runs a request's scripts around it: pre-request scripts before the variables are filled in, test scripts after the response, in the Sobek sandbox of [ADR-0017](0017-postman-scripts.md). Imported collections keep their scripts ([ADR-0031](0031-postman-import.md)), and until now nothing ran them.

Sending from the Requests page is a run of one request. Test suites, which run many and pass values between them, come next and will reuse what this decides.

## Decision

**What runs, in order.** Sending a saved request (`itemId`) runs, in one script session:
1. the pre-request scripts of the collection, then each folder from the top, then the request;
2. after the response, the test scripts in the same order.

The request's own scripts are the editor's, so unsaved edits run. Without them, the saved ones run. A request that isn't saved runs only its own. A send without scripts doesn't start a session.

**Pre-request scripts see the request as written**, with `{{variables}}` unfilled, as in Postman. They may change it, such as adding a header, and set variables. Then the variables fill in `{{name}}`, narrowest first: what scripts set locally, then the environment, then the collection. Auth is applied last. A pre-request script that throws stops the send: the result has no response and `failure.code` `script`, which names the script and the error. `pm.execution.skipRequest()` gives `skipped`. A test script that throws is reported, and the others still run.

**Scripts' variable changes last for the send.** In Postman, `pm.environment.set` changes the environment's current value, which is local to one person. Here environments belong to the project, so a login script that saves a token would change it for everyone. So changes are reported in the result and never saved. A run of a suite carries them from one request to the next, as Newman does. Saving them on purpose, such as a per-person current value, can come later if people need it.

**What comes back.** The result carries the scripts' tests (each with its script's name, whether it passed, and why not), console output, errors and variable changes. History keeps them, with secrets masked as everywhere else ([ADR-0029](0029-environment-variables.md)). That includes a secret a script logged, and the value a script set a secret variable to.

**`pm.sendRequest` goes through the runner**, so it reaches only what the egress policy allows ([ADR-0016](0016-runner-egress.md)). It isn't kept in history.

**Where scripts run, and how many.** Scripts run where requests are sent: in the process with the runner role. A pool keeps two sessions warm. At most four sends run scripts at once, because the memory watchdog watches the whole process (ADR-0017), so fewer scripts at once keep it accurate. A process without the runner role can't send at all, so it answers `runner-unavailable`, as for any send.

**Editing.** The editor has a Scripts tab with the request's pre-request and test scripts. Saving keeps them with the request (`scripts` on items). The response view has Tests and Console tabs, and shows script errors above them.

## Consequences

- Imported Postman collections now run their scripts on the Requests page, and their tests show beside the response.
- A login flow that stores a token with `pm.environment.set` works within a send, or a suite run, but the token isn't saved for the next separate send. The response says which variables changed and that they weren't saved.
- Collection and folder scripts are kept and run, but can't be edited in the web app yet. Their editor comes with test suites, and the API takes them through the import.

## Alternatives considered

- **Saving variable changes to the environment, as Postman's local current values do.** Rejected for shared environments: see above.
- **Running scripts in the browser.** Rejected: the sandbox, its libraries and the egress policy live on the server, and test suites run there and in CI without a browser.
- **A session per script.** Rejected: Postman runs a request's scripts in one context, and collections rely on helpers a collection script defines for the request's.
