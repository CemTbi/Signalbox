# 0048. Incidents open and resolve with their monitor's state

- Status: Accepted
- Date: 2026-11-11
- Deciders: project lead

## Context

[RFC 0002](../rfcs/0002-monitors-and-tracing.md) decided what an incident is:
- **Opening:** a monitor going `down` opens one, and a monitor has at most one open. Going `degraded` opens none.
- **What it records:** the first failing result, when it opened, who acknowledged it and when, and when and how it was resolved.
- **Resolving:** it resolves on its own when the monitor is `up` again. Resolving it by hand holds off new incidents until the monitor has been `up` once.
- **Acknowledging** stops repeat notifications.
- **The timeline** shows every state change, notification and acknowledgement, with who did it.

It left open where the decision is made, what happens to incidents when their monitor goes, and how they're listed. Notifications come with alert channels, the next step.

## Decision

**An incident changes in the same transaction as its monitor's state.** `keep`, which every check's result goes through, and a heartbeat's call both call `incidentStep` with the monitor's row locked:
- **Down, with none open and none held off:** opens one, copying the check that did it: status, failure, trace and run.
- **Up, with one open:** resolves it as `recovered`.
- **Any other change while one is open,** such as down to degraded: goes on its timeline.

Two schedulers, or a check and a person, can't open two incidents for one monitor. The row lock orders them, and a unique index on open incidents per monitor makes sure of it. An incident can't say a monitor is down when its state doesn't: there's no window between the two.

**Held off:** resolving by hand sets `incidents_held` on the monitor unless it's up. Its next time up clears it, so the next time down opens one. A person who resolves an incident on a monitor that's still failing has said they know, and shouldn't be paged again until the service has recovered.

**Acknowledging** records who and when, once. Acknowledging again changes nothing, and a resolved incident can't be acknowledged (`409 incident-resolved`). Resolving a resolved one changes nothing, so two people pressing Resolve at once both succeed.

**The timeline** is `incident_events`: opened, state, acknowledged, resolved, paused and resumed, each with who did it, or no one for Signalbox. Alerts will add `notified`. An incident's page reads its newest 500, since a monitor that flaps between down and degraded for days writes a line per change.

**A monitor's deletion** resolves its open incident as `deleted`, by whoever deleted it, and keeps its incidents: `monitor_id` becomes null, and `monitor_name` keeps what it was called. Deleting the project deletes them.

**Listing:** open incidents first, then newest first, by an index on project, state and ID. The cursor is an incident's ID, and the next page carries on from it in the same order. Filters are state and monitor. A monitor's `openIncidentId` links its page to its incident.

**Who may do what:** every member reads incidents; owners, admins and editors acknowledge and resolve, as the RFC's table says.

**The web app** has an Incidents page per project, listing each incident's status, open, acknowledged or resolved, when it opened, how long it lasted and what took the monitor down. An incident's page shows the failure, with Open trace or Open the run, the timeline, and Acknowledge and Resolve. A monitor that's down with an open incident says so on its page, with a link.

## Consequences

- An incident opens within its monitor's confirmation: its interval and 10 seconds at most after the service fails, as ADR-0046 measured.
- A resolved incident stays until its project is deleted; nothing removes old ones yet. A busy project's list grows by an incident per outage, which the index keeps cheap to page through.
- Pausing a monitor that's down leaves its incident open, with the pause on its timeline. Resuming starts it pending, and its next check decides.
- `signalbox_incidents_opened_total` counts incidents opened.

## Alternatives considered

- **Open incidents from a worker that watches state changes.** It would decouple the two, but a crash between the state change and the incident would lose the incident, and two workers would need their own lock.
- **One table, with the timeline as JSON on the incident.** Fewer rows, but every event rewrites the incident, and two writers race on the array.
- **Delete a monitor's incidents with it.** Simpler, but the history of an outage would vanish with the monitor that saw it, which is often the moment someone wants it.
- **Hold off new incidents for a set time after resolving by hand.** A time is a guess; "until it's been up once" is what the person meant.
