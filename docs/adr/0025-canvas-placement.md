# 0025. Placing services on the canvas

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

v0.1 places services on the canvas by hand. Every project page shows its canvas ([ADR-0018](0018-live-canvas.md)), and each service has a position, but until now the canvas showed nothing, and a position could only change through the service's PATCH, which needs its ETag.

A team arranges a canvas together. Moving a node is frequent and cheap to redo. Renaming or describing a service is rare, and losing such a change silently would be a real loss.

## Decision

**Layout has its own endpoint, `PUT /api/v1/projects/{projectId}/canvas`**, which sets the positions of up to 500 of the project's services at once.
- **Owners, admins and editors may move services.** Viewers see the canvas without dragging.
- **It needs no `If-Match`, and the last move wins.** If two people drag the same node at once, the later drop is where it stays, which both can see and redo.
- **Moving a node doesn't change the service's version.** A rename begun before a drag still applies, instead of failing with `412` over a move nobody meant to conflict with.
- **Moves are all or nothing.** If a service isn't in the project, nothing moves and the answer is `404`.

**New services take the next free spot**, on a grid four nodes wide with room for lines between them (288 by 152 canvas units). A position sent when creating a service still wins.

**The web app saves moves as they finish.** A drag saves when it's dropped. Moving a selected node with the arrow keys saves 400 ms after the last key, so a run of key presses is one request. Positions snap to an 8-unit grid, so saved positions are whole numbers and keyboard moves take even steps. **Arrange automatically** lays the canvas out with ELK, as in the spike, and saves every position in one request. Double-clicking a node opens its service.

## Consequences

- A canvas looks the same to everyone in the project, and keeps its layout across reloads.
- Two people dragging the same node at the same moment can't lose anything but a position.
- Lines between services, health, latency and spec versions on the nodes come with traces, monitors and declared dependencies, after v0.1.

## Alternatives considered

- **PATCH each service with its ETag.** Rejected: every drag would need the service's current ETag, an arrangement would be one request per service, and a drag would make a colleague's rename fail.
- **Store layout in the browser.** Rejected: the canvas is the team's shared picture of its system.
- **Lay out automatically on every load.** Rejected as the only way: people put services where they make sense to them. Automatic layout is a button.
