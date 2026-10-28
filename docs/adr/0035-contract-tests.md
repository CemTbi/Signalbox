# 0035. Contract tests from published versions

- Status: Accepted
- Date: 2026-10-28
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) generates contract tests from a published spec version: each operation gets a request filled from its examples, asserting a documented status and a response that matches its schema. The generated collection is linked to the version, and generating it again for a newer version updates it. Runs ([ADR-0033](0033-collection-runs.md)) and `signalbox test run` ([ADR-0034](0034-cli-test-run.md)) then run it like any suite.

Two questions decide the design. Where does the check run? And what happens to people's changes when the tests are generated again?

## Decision

**Requests remember their operation.** A saved request may carry a `contract`: a published version, a method and a path template. After its response, the engine checks the response against that operation, wherever the request runs: a send on the Requests page, or a run. The checks appear as tests from `Contract`, so they count and fail like script tests. They are:
- the operation is still in the version;
- the status is documented, exactly, as a range such as `4XX`, or by `default`;
- the body matches the status's schema.

**Checks run in Go, not in the sandbox.** `pkg/contract` validates with libopenapi-validator against the operation the request names, not the URL. So a run's `baseUrl` can point anywhere: staging, a local server, or the hosted mock. The sandbox's schema libraries (ajv 6, tv4) know JSON Schema draft-07 at most. OpenAPI 3.1 schemas are 2020-12, and 3.0's `nullable` and `$ref` rules differ from both. The apis module builds a checker per version once and keeps the 16 used last. A version of another project, or one deleted since, fails the check with a message saying so. Schemas too large to check fail the same way, under the limit mocks use.

**What's generated.** `POST /api/v1/versions/{versionId}/contract-tests` makes, from the version's reference ([ADR-0022](0022-reference-docs.md)):
- a collection named after the spec, with `baseUrl`: the spec's first server, or the version's hosted mock when it names none;
- a folder per tag, and untagged operations at the top;
- a request per operation, named by its summary:
  - path parameters as collection variables, from their example, default or first enum value;
  - required query and header parameters, with the same values;
  - the request body's example, JSON preferred, as the mock would send it;
  - auth from the operation's first way of authorizing that needs a scheme: bearer `{{token}}` for HTTP bearer, OAuth 2 and OpenID Connect, basic `{{username}}` and `{{password}}`, and an API key as `{{apiKey}}` in its header or query. These are meant to be set in an environment, where secrets belong.

**Generating again.** A spec's contract tests are one collection per project (`collections.contract_spec_id`). Generating again, from the same or a newer version:
- replaces the generated folders and requests (`collection_items.generated`);
- keeps the items people added, and the scripts and variables they set. Existing variables keep their values, and new ones are added.

Changes people make to generated requests are replaced. Collection and folder scripts and variables are where to customize generated tests. This is said wherever the tests are generated.

**Who.** Generating needs an editor's role in the version's project, like importing a collection.

## Consequences

- A team publishes a version, generates its contract tests, sets `baseUrl` and credentials in an environment, and gates CI on them with `signalbox test run --server`.
- Contract checks need the server, which holds the versions. A collection exported to Postman keeps its requests but not the checks, and offline `signalbox test run` doesn't check contracts.
- Generated requests that need real IDs, such as `{{orderId}}`, run against the mock as generated, and against a real API once their variables are set.

## Alternatives considered

- **Generate test scripts with `pm.response.to.have.jsonSchema`.** Rejected: the sandbox's validators can't read OpenAPI 3.1 schemas correctly, the schema would be copied into every script, and the tests would go stale silently when the spec changes.
- **Match responses by URL against the spec's servers.** Rejected: runs target staging, local servers and mocks, whose URLs aren't in the spec.
- **Merge regenerated requests with people's edits.** Rejected for now: a three-way merge of requests is hard to explain. Keeping people's own items and the variables covers the common case.
