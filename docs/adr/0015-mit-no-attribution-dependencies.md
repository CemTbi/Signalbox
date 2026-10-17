# 0015. MIT No Attribution in shipped dependencies

- Status: Accepted; amends [ADR-0008](0008-dependency-policy.md)
- Date: 2026-10-17
- Deciders: project lead

## Context

[ADR-0008](0008-dependency-policy.md) lists the licenses that shipped code may depend on. ClickHouse's Go client, which stores spans ([ADR-0014](0014-span-storage.md)), uses `github.com/segmentio/asm` for fast byte handling. That package is released under MIT No Attribution (MIT-0): the MIT license without its one condition, that copies keep the copyright notice. It is approved by the Open Source Initiative. ADR-0008's list includes 0BSD, the BSD license without conditions, but not MIT-0.

## Decision

Shipped code may also depend on packages under MIT-0. Like 0BSD, it grants every right and asks for nothing in return, so it is compatible with Apache-2.0 and needs no notice in the distribution.

The license checks follow this list. `web/scripts/check-licenses.mjs` allows MIT-0 for the web app. go-licenses doesn't recognize MIT-0 and reports such a package's license as unknown, so the CI job names each Go module under MIT-0 with `--ignore`, after someone has read its license: today only `github.com/segmentio/asm`.

## Consequences

- Packages under MIT-0 can be used without an exception in the policy, though each Go module needs its line in CI.
- An ignored module's license isn't checked again when it updates. Updates to `github.com/segmentio/asm` need a look at its license before they merge.
- Nothing changes for the notices Signalbox ships: MIT-0 doesn't require one.
