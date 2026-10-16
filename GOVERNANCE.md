# Governance

This document describes who makes decisions in the Signalbox project and how.

## The project today

Signalbox has one maintainer, listed in [MAINTAINERS.md](MAINTAINERS.md), who decides alone. Most code is written with Claude, Anthropic's AI coding assistant: it implements changes in pull requests, its commits carry a `Co-Authored-By: Claude` trailer, and the maintainer reviews and merges every pull request. RFCs and ADRs are still written, and they are accepted when the maintainer merges them, so the reasoning behind decisions stays on record.

The rules below take effect once the project has three maintainers. Until then they describe where the project is heading.

## Roles

**Users** run Signalbox and report problems or ideas. Everyone is welcome to.

**Contributors** send pull requests, review, write documentation, answer questions or triage issues. Anyone who does any of these is a contributor.

**Maintainers** review and merge pull requests, cut releases, and decide the project's direction. They are listed in [MAINTAINERS.md](MAINTAINERS.md). Each maintainer is responsible for at least one area and is a code owner for it.

## How decisions are made

Most decisions happen in pull requests and issues through **lazy consensus**: a proposal goes ahead if no maintainer objects within a reasonable time, normally three working days for anything non-trivial.

**Significant changes** need an [RFC](docs/rfcs/README.md). That covers new modules, changes to the public API, the data model or the security model, new runtime dependencies in shipped artifacts, and changes to this document. An RFC is accepted when at least two maintainers approve it and no maintainer objects after a final comment period of seven days.

**Architecture decisions** are recorded as [ADRs](docs/adr/README.md), so the reasoning outlives the discussion.

When consensus can't be reached, maintainers vote. A simple majority of all maintainers decides. Changes to this document need two thirds.

## Becoming a maintainer

A contributor becomes a maintainer after sustained, quality contributions in an area, usually over at least three months, and after showing good judgment in reviews. Any maintainer can nominate a contributor. The nomination passes with the approval of a majority of maintainers and no veto from the maintainers responsible for that area.

## Stepping down

Maintainers who can no longer contribute should step down. They move to an emeritus list and can return through a fast nomination. A maintainer who has been inactive for six months is asked whether they want to continue.

## Code of conduct

The [code of conduct](CODE_OF_CONDUCT.md) applies to everyone. Maintainers enforce it. Reports are handled confidentially, and a maintainer who is involved in a report takes no part in handling it.

## Changes to this document

Changes to governance follow the RFC process and need two thirds of maintainers to approve.
