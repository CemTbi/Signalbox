# 0008. Dependency policy

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Every dependency we ship becomes part of what users install and trust. A license that conflicts with Apache-2.0, an abandoned package or a compromised release harms every installation. Dependencies also decide binary size and build times.

## Decision

**Licenses.** Code we ship (the binaries, the container image and the web app bundle) may only depend on these licenses:

- MIT, BSD-2-Clause, BSD-3-Clause, ISC, Apache-2.0, 0BSD, Zlib, Unicode
- MPL-2.0 and EPL-2.0, used unmodified
- OFL-1.1 for fonts, and the MIT-style Lucent license of the classic dtoa routines. Both arrive through vacuum's dependencies today: chroma bundles a font notice, and goja's `ftoa` package carries the Lucent license.

GPL, LGPL, AGPL, SSPL, BSL, the Commons Clause and proprietary licenses are not allowed in shipped code. Tools that only run in CI and are never distributed, such as k6 (AGPL-3.0) for load tests, are allowed. CI checks Go dependencies with go-licenses and web dependencies with a license checker, and fails on anything outside the list.

**Choosing a dependency.** Before adding one, check that it:

- is maintained, with a release or meaningful commits in the last 12 months
- is widely used, or small enough that we could take it over
- has a reasonable security posture; the OpenSSF Scorecard is a useful signal
- doesn't pull in a large tree for a small feature; prefer the standard library

A new runtime dependency in shipped code needs a sentence in the pull request on why, and maintainer approval. A dependency that pulls in a large subtree needs an RFC.

**Keeping them current.** Renovate opens grouped update pull requests every week and security updates at once. GitHub Actions are pinned to commit digests. `go.sum` and `package-lock.json` are committed. govulncheck and osv-scanner run on every pull request.

## Consequences

- Releases stay legally clean for companies that check licenses.
- Updates arrive in small, regular batches instead of risky jumps.

## Watch list

- **vacuum's dependency tree.** `vacuum/motor` imports `vacuum/utils`, which imports `pb33f/doctor`. That brings in terminal UI libraries and a pure-Go SQLite, and is most of the CLI's 53 MB. Next step: propose separating them upstream. See [ADR-0003](0003-spec-engine.md).
