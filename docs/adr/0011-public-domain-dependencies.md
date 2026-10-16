# 0011. Public-domain dedications in shipped dependencies

- Status: Accepted; amends [ADR-0008](0008-dependency-policy.md)
- Date: 2026-10-16
- Deciders: project lead

## Context

[ADR-0008](0008-dependency-policy.md) lists the licenses that shipped code may depend on. The web app's router, TanStack Router, depends on `isbot`, which is released under the Unlicense. The Unlicense, like CC0-1.0, isn't a license with conditions but a dedication to the public domain, and ADR-0008's list doesn't mention either.

## Decision

Shipped code may also depend on packages under the Unlicense or CC0-1.0. Both waive copyright and impose no conditions, so they are compatible with Apache-2.0 and need no notice in the distribution.

The license checks follow this list: `web/scripts/check-licenses.mjs` for the web app, and go-licenses for Go, which counts both dedications as unencumbered.

## Consequences

- Common small packages released into the public domain can be used without an exception each time.
- In jurisdictions that don't recognize public-domain dedications, CC0 and the Unlicense fall back to a permissive license, so the risk stays low.
