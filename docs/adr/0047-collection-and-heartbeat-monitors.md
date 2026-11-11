# 0047. Collection and heartbeat monitors

- Status: Accepted
- Date: 2026-11-11
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) asks for two more kinds of monitor beside HTTP and health checks ([ADR-0046](0046-monitors.md)):
- **Collection:** runs a collection or folder with the v0.2 engine ([ADR-0033](0033-collection-runs.md)). It passes when the run passes, and every step's results are kept as for any run.
- **Heartbeat:** sends nothing. A job calls `POST /api/v1/heartbeats/{token}`, or `GET`, when it finishes, and the monitor goes down when no call comes within its interval and a grace period. The token is long and random, kept as a hash, limited per token and rotatable.

The RFC left open how a monitor's runs live beside people's, how long a run may take, when a heartbeat is late, and what happens to a call that arrives while a missed heartbeat is being recorded.

## Decision

**A collection monitor's check is a test run.**
- **Through the run engine:** `testruns.RunForMonitor` plans the collection, or the folder, for no one, fills in the environment's variables and secrets, and runs it through the server's runner with its scripts. It runs in the scheduler's check slot, in the scheduler's goroutine, not in the two run slots people's runs wait for, so a busy afternoon of runs never delays a monitor, nor a monitor someone's run.
- **Kept as any run:** each run is a `test_runs` row with `monitor_id` and every step, secrets masked, so the run's page, its steps and its reports work as they do for a person's. Its check result names it with `runId`, in ClickHouse and as the monitor's last check.
- **Not the collection's:** a collection's list of runs, and its 100 newest kept, leave monitor runs out, so a one-minute monitor doesn't push a person's runs out of the history.
- **Kept per monitor:** its 50 newest passed runs and its 50 newest failed ones, and none past the 90 days runs are kept. A failure's run outlives the passes after it, so the run behind an incident is still there the next morning. Older check results keep their `runId`, and its run may be gone.
- **Judged:** it passes when the run passes: every request got a response, every test passed and no script failed. A failure names the first step that failed, and how: a request with no response, a script's error, or `2 of 9 tests failed, first "is 201" in Orders / Create order: expected 500 to equal 201.` Its status and trace ID are that step's, so Open trace goes to the request that failed.
- **Time:** the monitor's timeout is the whole run's, 1 to 120 seconds, 60 by default. Running out fails the check with `timeout`, even when the step that was running ends without an error of its own. Check now waits for the run, so its response may take as long, and it lengthens its own write deadline past the server's 60 seconds.
- **Saying nothing:** an empty collection, or one over the 10,000-request limit, is skipped, as is a run someone cancels from its page.
- **Deleting** the collection, or the folder, deletes the monitor: there's nothing left for it to check.

**A heartbeat is a deadline.**
- **The token:** `sbx_hb_` and 43 random characters, shown once when the monitor is made and when it's rotated. It's stored as a SHA-256 hash, as ingest keys and access tokens are. Rotating replaces it at once.
- **A call** marks the monitor up, records a passed check, and moves `next_check_at` to the call's time plus the interval and grace. A paused monitor takes the call and changes nothing, so a paused job's calls don't fail.
- **No call** by then is found by the scheduler as any due monitor, and records a failed check, `no-heartbeat`, at once: the grace is the confirmation, so a heartbeat's `failuresToConfirm` is 1. While no call comes, it's recorded again each interval.
- **A call while a miss is recorded wins.** The miss is decided in one transaction that locks the monitor's row, which a call also locks, and it's recorded only when the last call came before the deadline the scheduler claimed. A call that came after the claim leaves the monitor up.
- **The clock starts** when the monitor is made or resumed: the job has a whole interval and grace for its first call. Changing the interval or grace counts from the last call, so a shorter grace on a job that's already late fails it at once.
- **Intervals:** a minute to 30 days, a day by default, and grace up to 7 days, as long as the interval by default, at most an hour. The RFC's 30 seconds to an hour is for checks Signalbox sends. A heartbeat watches jobs, which are mostly nightly, weekly or monthly, and an hour would leave them out.
- **Limits:** 10 calls at once and then 10 a minute per token, and 120 at once and then 10 a second per address, so guessing tokens is slow. A call needs no sign-in and no CSRF token: the token is the credential, as the RFC says. Its body is never read.
- **Logs:** the access log writes `/api/v1/heartbeats/{token}` for every call, never the token. A reverse proxy in front of Signalbox logs paths of its own, which its operator must keep as private as the tokens; the installation guide, when it comes, must say so.

**A monitor's kind changes only between `http` and `health`.** A collection monitor and a heartbeat have different fields, results and meaning, and changing one into another would make its history say something it didn't check. Each kind refuses the others' fields: a request and assertions belong to HTTP and health checks, a collection and folder to collection monitors, and grace to heartbeats.

**The database** gains `collection_id`, `folder_id`, `heartbeat_token_hash`, `grace_seconds`, `last_ping_at` and `last_run_id` on `monitors`, with checks per kind, and `monitor_id` on `test_runs` (PostgreSQL migration 00019). `check_results` gains `run_id` (ClickHouse migration 00005).

**The web app** asks for a monitor's kind first and shows its fields: a collection and a folder, or a heartbeat's interval and grace in minutes. A heartbeat's page shows its URL with the token once, from memory only, so a reload loses it as the API means it to, and Rotate token asks before it breaks the job's URL. A collection monitor's checks link to their runs, and a run says the monitor ran it.

## Consequences

- A collection monitor costs a run's storage per check: a one-minute monitor writes a run a minute, and keeps at most 100.
- A collection monitor of a large collection takes a check slot for as long as its run, up to two minutes. A process runs 200 checks at once.
- Monitor runs run scripts, in the same sandbox and pool as people's runs, so many collection monitors with heavy scripts compete with people's sends for script slots.
- A heartbeat's results are its calls, so its response time chart says nothing, and its uptime is the share of calls and misses.
- A token in a URL can end up in a proxy's or a job's logs. That's the price of a URL a cron job can call with `curl`; rotating is the remedy.
- Each process limits heartbeat calls on its own, as every rate limit does until NATS.

## Alternatives considered

- **Queue a collection monitor's run with people's.** One queue is simpler, but two slots per process would delay monitors behind people's runs, and the check start delay target would be meaningless.
- **Keep monitor runs among the collection's runs.** A one-minute monitor would replace a person's 100 newest runs in under two hours.
- **Keep only a monitor's newest runs, passed or not.** Fewer rows, but the run behind a failure at night would be gone by morning.
- **A heartbeat that waits for its first call before it can go down,** as some services do. It would never tell anyone about a job that never ran, the mistake a new monitor is most likely to catch.
- **Confirm a missed heartbeat 10 seconds later, as other failures are.** A call that didn't come in its grace won't come in 10 seconds more; the grace is the confirmation.
- **The token in a header,** so it stays out of URLs. Many jobs can only fetch a URL, and the RFC chose the path.
