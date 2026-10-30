# 0037. The local agent asks for work over HTTPS

- Status: Accepted
- Date: 2026-10-30
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) adds `signalbox agent`, which sends a person's requests from their own machine, so `localhost`, private networks and VPN-only hosts work. The agent must open no inbound port and must only ever receive its owner's requests. The RFC sketched an outbound WebSocket to `/api/v1/agents/connect`.

A WebSocket needs a new dependency ([ADR-0008](0008-dependency-policy.md)), an upgrade path through the middleware and every proxy in front of Signalbox, and a protocol of its own for jobs, results, keep-alives and reconnects. The server already speaks JSON over HTTPS, and the CLI already has a client for it with a personal access token ([ADR-0034](0034-cli-test-run.md)).

## Decision

**The agent long-polls.** It is a plain API client with a personal access token:
- `POST /api/v1/agents` registers it by name, or finds the owner's agent with that name, so a restart is the same agent.
- `POST /api/v1/agents/{agentId}/jobs/next?waitMs=25000` waits up to 25 seconds for a job, and answers `204` when none came. Asking marks the agent online for 45 seconds.
- `PUT /api/v1/agents/{agentId}/jobs/{jobId}` returns the response, or why there was none.

Jobs and results carry bodies in base64, so binary bodies survive JSON. A result may be 16 MiB, room for the runner's 10 MiB body.

**Only the owner's requests reach an agent.** Every agent call checks that the agent is the caller's. A send or a run names an agent with `agentId`, and the server finds it among the sender's agents only; someone else's agent is not found. Secrets reach the agent decrypted, over TLS, only for requests its owner sends. History masks them as it does for the server's sends, and records which agent sent each request; a run records its agent too.

**Jobs live in the process that serves the API**, in a queue per agent of at most 64. A send waits 15 seconds for the agent to take its job, then fails as `agent-offline`. Once taken, the result must come within the request's timeout plus 10 seconds, or the send fails as `agent-timeout`. Jobs carry the server's 30-second timeout, so the agent gives up when the server would, and a send always answers within 55 seconds, inside the server's 60-second write timeout. A sender that gives up leaves its job behind; the agent skips such jobs, and a full queue drops them before it refuses a new one as `agent-busy`.

**The agent's network is its owner's.** It may reach any address, including loopback and private ranges; `--deny` keeps requests from ranges its owner chooses. It sends up to 8 requests at a time (`--concurrency`), backs off up to 30 seconds while the server can't be reached, reconnects when its agent was deleted, and stops on Ctrl-C after returning the results it has.

## Consequences

- No new dependency, and nothing new for proxies: the agent's traffic is ordinary HTTPS requests that last at most 25 seconds.
- A job waits for the agent's next poll. The agent always has a poll open, so this costs a round trip, not a polling interval.
- An agent and the sends for it must reach the same server process. That holds for `--roles=all` and one API process. Several API processes behind a load balancer need the queue moved to PostgreSQL (`LISTEN`/`NOTIFY`) or the agent pinned to one process; that is left for when Signalbox scales out.
- A deleted agent that is still running connects again as a new agent. To stop it for good, its owner revokes its token.
- Scripts' `pm.sendRequest` calls go through the same agent as the request that runs them.

## Alternatives considered

- **A WebSocket**, as the RFC sketched. Rejected for now: a new dependency and protocol for no gain at this scale. Long polling can be replaced later behind the same module without changing the web app.
- **Server-sent events for jobs, with results by `PUT`.** Rejected: one long-lived stream needs its own keep-alives and proxy settings, and gains little over a poll that is always open.
- **Running requests in the browser.** Rejected in the RFC: CORS blocks most APIs, and secrets would reach the browser.
