# Architecture decision records

An architecture decision record (ADR) captures one significant decision: the context, what we decided, and what follows from it. ADRs are never edited after acceptance except to change their status. A new decision that replaces an old one gets a new ADR that supersedes it.

Write a new ADR by copying [template.md](template.md) to the next number. Propose it in a pull request; it is accepted when the pull request merges.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-single-binary-with-roles.md) | One server binary with six roles | Accepted |
| [0002](0002-data-stores.md) | PostgreSQL, ClickHouse, NATS and object storage | Accepted |
| [0003](0003-spec-engine.md) | Spec engine on libopenapi and vacuum | Accepted |
| [0004](0004-authentication-and-sessions.md) | Authentication, sessions and API tokens | Accepted; amended by 0010 |
| [0005](0005-http-api-conventions.md) | HTTP API conventions | Accepted |
| [0006](0006-frontend-architecture.md) | Frontend architecture | Accepted |
| [0007](0007-license-and-dco.md) | Apache-2.0 license and DCO sign-off | Accepted |
| [0008](0008-dependency-policy.md) | Dependency policy | Accepted; amended by 0011 and 0015 |
| [0009](0009-database-conventions.md) | Database conventions | Accepted |
| [0010](0010-first-run-setup.md) | First-run setup, the public URL and request origins | Accepted |
| [0011](0011-public-domain-dependencies.md) | Public-domain dedications in shipped dependencies | Accepted |
| [0012](0012-invitation-links.md) | Invitation links instead of email | Accepted |
| [0013](0013-single-sign-on-accounts.md) | Single sign-on accounts through OpenID Connect | Accepted |
| [0014](0014-span-storage.md) | Span storage in ClickHouse | Accepted |
| [0015](0015-mit-no-attribution-dependencies.md) | MIT No Attribution in shipped dependencies | Accepted |
| [0016](0016-runner-egress.md) | Where the runner may send requests | Accepted |
| [0017](0017-postman-scripts.md) | Running Postman scripts in Sobek | Accepted |
| [0018](0018-live-canvas.md) | A live canvas of 200 services | Accepted |
| [0019](0019-spec-revisions.md) | Services, specs and their revisions | Accepted |
| [0020](0020-published-versions.md) | Published versions and the publish gate | Accepted |
| [0021](0021-hosted-mocks.md) | Hosted mocks of published versions | Accepted |
| [0022](0022-reference-docs.md) | Reference docs of published versions | Accepted |
| [0023](0023-breaking-changes.md) | Breaking-change reports with oasdiff | Accepted |
| [0024](0024-code-generation.md) | Code generation, starting with Go | Accepted |
| [0025](0025-canvas-placement.md) | Placing services on the canvas | Accepted |
| [0026](0026-personal-access-tokens.md) | Personal access tokens | Accepted |
| [0027](0027-runner-and-history.md) | The runner role and request history | Accepted |
| [0028](0028-collections.md) | Collections of saved requests | Accepted |
| [0029](0029-environment-variables.md) | Environment variables and secrets | Accepted |
| [0030](0030-request-auth.md) | Request auth | Accepted |
| [0031](0031-postman-import.md) | Postman and cURL import and export | Accepted |
| [0032](0032-scripts-in-sends.md) | Running scripts when a request is sent | Accepted |
| [0033](0033-collection-runs.md) | Running collections as test suites | Accepted |
| [0034](0034-cli-test-run.md) | `signalbox test run` | Accepted |
| [0035](0035-contract-tests.md) | Contract tests from published versions | Accepted |
| [0036](0036-ci-templates.md) | CI templates for test suites | Accepted |
| [0037](0037-local-agent.md) | The local agent asks for work over HTTPS | Accepted |
