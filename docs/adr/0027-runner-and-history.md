# 0027. The runner role and request history

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) puts sending requests in the `runner` role, behind a `Jobs` interface, and keeps each person's sends as history. This is the first step that sends requests on someone's behalf from the server. Several details the RFC left open need settling: who may send, how the runner is protected from reaching Signalbox's own services, what history keeps, and how failures are reported.

## Decision

**The runner role is available, and runs with the api role.** `--roles=all` now means api and runner, and the api role calls the runner in process (`internal/modules/runner`). A process with the runner role but not the api role refuses to start, because runners in their own processes need NATS, which comes with the high-availability work.

**Owners, admins and editors may send; viewers may not.** The server sends on the person's behalf, to whatever their environments point at, production included. Viewers can read their own history, which is empty unless their role changed.

**The egress policy protects the database by address and by name.** At startup, the database URL's hosts are resolved, and their addresses and ports become `Protected` ([ADR-0016](0016-runner-egress.md)). The hostnames themselves are denied too, in case they move. If the database host can't be resolved, startup fails rather than run unprotected. Admins adjust the policy with `SIGNALBOX_EGRESS_ALLOW` and `SIGNALBOX_EGRESS_DENY`: comma-separated address ranges, addresses and hostnames such as `*.internal.example.com`.

**A request that gets no response is still a successful send.** `POST /api/v1/projects/{projectId}/send` answers `200` with either `response` or `failure`. The failure has a code (`invalid-request`, `blocked`, `dns`, `connect`, `tls`, `timeout`, `network`) and a message a person can act on, such as "The host nowhere.example doesn't exist". HTTP errors from the API itself, such as `403` or `429`, stay for problems with the send.

**The runner keeps the start of long bodies.** It reads at most 10 MiB of a response and says it cut the rest, instead of failing the request; the egress client's own limit is lifted for it.

**Timings come from `net/http/httptrace`:**
- DNS, connect and TLS add up across redirects;
- first byte is measured from the request being written;
- download is reading the body.

Phases that didn't happen, such as TLS on a reused connection, are zero, and the web app shows them as a dash.

**History keeps what's useful and not what's secret:**
- **The request:** headers, with the values of `Authorization`, `Proxy-Authorization` and `Cookie` replaced by `•••`, and the first 64 KiB of the body.
- **The response:** headers and metadata, and the first megabyte of the body.
- **Retention:** 30 days and 1,000 entries per person and project. The newest are kept on every insert, and an hourly sweep deletes old ones.
- **Who sees it:** each person sees only their own history, and loses it in a project they leave.

Reopening an entry in the web app leaves credential headers empty, to be typed again, rather than sending dots.

**Sends are limited per person,** to 30 at once and then 10 a second.

## Consequences

- People can call APIs from Signalbox. Saved requests, environments and secrets arrive with collections, in the next step.
- A deployment can't split api and runner yet. The error says to run them together.
- Typed credentials aren't kept at rest. They come back from secrets once environments exist.

## Alternatives considered

- **Let viewers send.** Rejected: sending acts on the team's environments, which is more than viewing.
- **Answer failed sends with `502`.** Rejected: the send worked, and the client shows the failure as a result, as Postman does. Gateways and retries also treat `502` as the API's own fault.
- **Keep credentials in history.** Rejected: history is for seeing what happened. Secrets will live in environments, encrypted.
