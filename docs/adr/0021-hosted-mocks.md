# 0021. Hosted mocks of published versions

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

v0.1 is done when a frontend developer builds against a mock without help from the backend team. The build plan asks for:
- one URL per mock;
- responses from examples, or generated from schemas, with the same data for the same request;
- requests checked against the spec, with errors that name the field;
- the status chosen with `Prefer: code=404`;
- under 10 ms added at p99.

Mocks need a stable document to serve: published versions ([ADR-0020](0020-published-versions.md)).

## Decision

**The address is `{public URL}/mocks/{specId}/{version}/…`**, where `version` is a version's name or `latest`, the newest version. The rest of the path is the mocked API's path; a server URL's base path, such as `/v1`, may be included or left out. `latest` can't name a version.

**Mocks need no sign-in and allow any origin.** Frontends call them from their own development servers, with no Signalbox session. CORS preflights are answered, and `Preference-Applied` is exposed.
- **What's exposed:** the published spec's paths and examples are readable by anyone who can reach the server and knows the spec's ID. That matches mocks' purpose on internal networks. Installations exposed to the internet can block `/mocks/` at their proxy until per-project mock access arrives.
- **Rate limit:** 300 requests at once, then 100 a second, per client address.

**`pkg/mockengine` answers requests**, built on libopenapi's mock generator and libopenapi-validator, the libraries the spec engine already uses. It is a public package, so the CLI can run the same mocks locally.

**Requests:**
- **Checking:** each request is matched to an operation and checked against it. An invalid one gets `400 invalid-request`, with an `errors` list naming each problem's location, such as `query parameter limit` or `/qty`.
- **Unknown paths and methods:** an unknown path gets `404 no-such-path`, and a known path with the wrong method `405 method-not-allowed` with `Allow`.
- **Body size:** bodies over 1 MiB are refused.

**Responses:**
- **Choosing the response:**
  - the operation's lowest 2xx;
  - or the one `Prefer: code=…` asks for, including through ranges such as `4XX`;
  - a request that names a code isn't checked, so clients can see error examples.
- **Choosing the content:**
  - the content type follows `Accept`, preferring JSON;
  - `Prefer: example=name` picks a named example;
  - `Preference-Applied` says which preferences were used.
- **Building the body:**
  - a named or the first example, or the media type's `example`;
  - otherwise the schema's examples;
  - otherwise data generated from the schema, seeded from the method and path, so the same request gets the same data.
- **Text bodies** stay text.

**Built mocks are cached** by revision, 64 at most. Concurrent first requests share one build, and a revision never changes, so nothing goes stale. Mocks are served by the api role for now; a separate mock role, as ADR-0001 plans, can take them over without a change to their URLs.

**Swagger 2.0 versions** answer `501 mock-unsupported`, because the request validator reads only OpenAPI 3.

## Results (2026-10-17)

On an AMD Ryzen 5 3600, a cached mock answers a GET with a generated array of objects in 0.16 ms. A POST with its body checked against the schema takes the same. That is well under the 10 ms target.

## Consequences

- A frontend can start as soon as a spec version is published, and the address `…/latest` follows new publishes without reconfiguration.
- Anyone who can reach the server can read published specs through their mocks. Per-project access control for mocks, such as a token in a header, is the next step for internet-facing installations.
- Mocks remember nothing between requests. A stateful mode that keeps created resources is planned after 1.0.
- Swagger 2.0 specs need converting to OpenAPI 3 before they can be mocked.

## Alternatives considered

- **Mocks behind sign-in.** Rejected. Frontend development servers can't send a Signalbox session, and a token in every request is friction during prototyping. It will be an option per project.
- **A subdomain per mock**, such as `orders.mocks.example.com`. Deferred. It needs wildcard DNS and certificates; paths work on every installation. The plan keeps subdomains as an option.
- **Prism or another mock server beside Signalbox.** Rejected. Another runtime to install, and it would read documents differently from the spec engine.
- **Random data on every request.** Rejected. Stable data makes screenshots and tests repeatable.
