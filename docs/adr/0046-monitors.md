# 0046. Monitors: a scheduler in PostgreSQL, results in ClickHouse

- Status: Accepted
- Date: 2026-11-04
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) asks for HTTP and health checks on a schedule, with a target: a check starts within 2 seconds of its time at p99, for 1,000 monitors at a 1-minute interval on one machine. A failure must be confirmed before a monitor goes down, and a failing health check must reach Slack within two minutes by the end of v0.3. This is the part up to a monitor's state and results. Collection monitors, heartbeats, incidents and alerts follow.

The RFC decided the shape: a row per monitor with `next_check_at`, claimed with `FOR UPDATE SKIP LOCKED`, and results in ClickHouse. It left open what counts as a failure of the service, how a confirmation fits the schedule, and how results reach ClickHouse.

## Decision

**A `monitors` module and the `scheduler` role.** Every process with the role runs the scheduler. Until NATS, it hands checks to a runner in its own process, so it needs the `runner` role, as the runner needs `api`.

**Claiming.** Every second, one statement takes the due monitors there's room for, and moves each on to its next time.
- **Several schedulers** never start the same check, because the row is locked and moved in one statement.
- **Time missed** while nothing ran is skipped, not caught up. A scheduler back after an hour sends one check per monitor, at the same place in its interval.
- **Spread:** a monitor's place in its interval comes from the random part of its ID.
- **Slots:** a process runs at most 200 checks at once. What doesn't fit stays due for the next process or second.

**A confirmation is outside the schedule.** A failure sets `confirm_at`, 10 seconds on, and the schedule stays as it was, so a monitor keeps its place in its interval whatever happens to it. Once a monitor is down, it's checked at its interval without confirming again.

**A check still running** is `running_since` on the row. If the next check comes due while it's set, that one is recorded as skipped. A process that stops mid-check leaves it set, so it only counts for the timeout and 30 seconds more.

**What says something about the service, and what doesn't:**
- **Failed:** no response, an assertion that doesn't hold, or a status other than 2xx unless a status is asserted. A health format saying the whole is down also fails it, even with a 200.
- **Degraded, at once:** a health format that warns, a component down while the whole is up, or a certificate expiring within the warning.
- **Skipped, changing nothing:** the check before it was still running, or Signalbox couldn't try, such as when it can't read the environment's secrets. A process stopping mid-check records nothing at all. None of these is the service's fault, and alerts must not say so.
- **A health body in no format it reads** says nothing, and the check rests on its status and assertions.

**Checks go through the client's engine,** as requests and runs do, with the environment's variables and secrets filled in, and a `traceparent`. `client.Probe` does it for no one and keeps no history. It returns the response with secrets masked, and assertions and messages use that copy, so a failure message can't quote a secret. Assertions on a secret's value can't hold, and that's the reason.

**Assertions** are status, header, an RFC 9535 JSONPath and response time. JSONPath uses `speakeasy-api/jsonpath` (Apache-2.0), already in the build through `libopenapi`, so it adds no code to the binary. It queries YAML nodes, so the body is checked and compacted as JSON first, since compact JSON is YAML. It reads bodies up to 1 MiB, because a document is parsed on every check.

**Results go to ClickHouse a batch a second,** since it prefers batches to a row per check.
- **If ClickHouse is down,** a process holds up to 15,000 results, about 15 minutes of 1,000 one-minute monitors, then drops new ones and counts them. States keep working, because the scheduler never reads ClickHouse.
- **Retention:** 90 days, set in the table, as the RFC says.
- **Delay:** a result can be a second from readable, so the check's own page may show it on its next refresh.

**PostgreSQL keeps each monitor's current state and last result,** which is all the scheduler, the list and the canvas need. A check isn't an edit, so a monitor's version and ETag stay, and someone editing it isn't interrupted by its checks.

**On the canvas,** `canvas/live` gains each service's `health`: the worst of its monitors in that environment. A monitor never checked says nothing, nor does a paused one. `healthDetail` says what isn't well in words, shown as the node's tooltip.

**Who may do what:** every member reads monitors and results. Owners, admins and editors change them and check them now, which counts against their sends, since it sends a request.

**Limits:** 1,000 monitors per project, an interval of 30 seconds to an hour, and a timeout of 1 to 30 seconds, the runner's limit.

## Measurements (2026-11-04)

`SIGNALBOX_MONITOR_BENCH=1 go test ./internal/modules/monitors -run KeepsUp` runs 1,000 monitors at a 1-minute interval for two minutes, on the machine of [ADR-0014](0014-span-storage.md), with PostgreSQL and ClickHouse in containers on the same machine. Each check takes 100 ms in a stand-in for the runner, so this measures the scheduler and its stores, not the network.

| 2,084 checks | Start delay |
|---|---|
| p50 | 508 ms |
| p99 | 992 ms |
| Worst | 2.27 s |

The target is 2 seconds at p99. Most of the delay is the one-second tick, which is why the median is half a second. `signalbox_monitor_check_start_delay_seconds` shows the same on a real installation.

## Consequences

- A monitor reaches `down` within its interval and 10 seconds of the service failing, which leaves room in the two-minute budget for incidents and alerts.
- A check that fails while a process stops is lost, and the next one decides. That's a later confirmation, not a false alarm.
- A process keeps results in memory for a second, so a crash loses that second's results. The state in PostgreSQL is still right.
- Members who may read a monitor can read its request, like a saved request. Credentials belong in the environment's secrets, as `{{name}}`.
- Editing a monitor's request or assertions forgets its failures so far, since they were failures of something else. Its state stays until the next check.
- Changing the interval moves the monitor to its place in the new interval.
- Check now counts like a scheduled check. Two in a row can take a monitor down, which is what a person asking twice has confirmed.
- A monitor's environment can't change, because its results and state belong to it.
- The health of a service is only as good as its monitors: a service with none says "No checks".

## Alternatives considered

- **Compute the next time from the last check's end.** Monitors would drift together as checks vary, and the spread would be lost.
- **Keep confirmations in `next_check_at`.** One column is simpler, but a confirmation would move the monitor's place in its interval, and restoring it needs the other column anyway.
- **Count Signalbox's own failures as failures.** A lost database connection would page people about every service at once. The results say skipped instead, with the reason.
- **Write each result to ClickHouse as it comes, with async inserts.** It works, but the batch is a few lines, and it's where a result waits while ClickHouse is away.
- **Read uptime from PostgreSQL.** Decided against in the RFC: 1.4 million rows a day.
- **A chart library for the response times.** One bar per check needs a little CSS, and the table beside it says the same in words.
