# 0023. Breaking-change reports with oasdiff

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

v0.1 promises lint and diff in CI. A team changing an API needs to know, before it merges, which changes break the API's clients. The requirements for 1.0 ask for breaking-change reports between versions, identical checks in the editor, the CLI and CI, and a diff between any two revisions.

A useful report:
- names the operation and the line of each change, so CI can annotate the pull request;
- says what changed in words, such as "the `query` request parameter `limit` became required";
- separates changes that break clients from those that might and those that don't.

libopenapi, which the spec engine, mocks and docs use, has a document comparison (`what-changed`), and we tried it first. Its changes carry lines but not the operation or the path to the change, so a report can't say where in the API a change is. Its rules also treated a parameter that gained `required: true` as non-breaking, which is the most common breaking change of all.

## Decision

**`pkg/specdiff` uses oasdiff's checks.** oasdiff (Apache-2.0) has over 300 checks, each with a stable ID, a level and a message. Every change names its operation, and has lines and columns in both documents. It handles OpenAPI 3.0 and 3.1, and Swagger 2.0 after kin-openapi converts it. Levels map to ours:
- `error`: a breaking change;
- `warning`: a change that may break some clients;
- `info`: a change that doesn't break clients.

**oasdiff reads documents with kin-openapi, a second OpenAPI parser.** Everything else keeps using libopenapi. The two can disagree about unusual documents, so a comparison reports only what oasdiff reads, and the spec engine stays the authority on whether a document is valid. Writing and maintaining 300 checks on libopenapi ourselves would take months, and they would be less tested than oasdiff's.

**Comparisons are bounded.** oasdiff expands references in place, like libopenapi ([ADR-0021](0021-hosted-mocks.md)). At 16 levels of a schema that refers to the next twice, it reports 65,536 changes in a second, and at 30 it doesn't finish. Before comparing, `pkg/specsize` counts each document's schemas with references expanded, the same count mocks use. A document past 100,000 is refused with `ErrTooLarge`.

**References:** file references resolve relative to each document. Remote references need `--allow-remote-refs`, as in `signalbox lint`.

**`signalbox diff <base> <head>`** prints `text`, `json`, `github` (annotations on the pull request) or `markdown` (a table for job summaries and comments). It fails with exit code 1 on breaking changes by default; `--fail-on` and `--min-level` change the thresholds. Either document can come from standard input, so CI can compare against another branch with `git show`.

**Signalbox's own CI reports changes to `api/openapi.yaml`** on every pull request, as annotations and in the job summary. Before 1.0 the API may break, so the report doesn't fail the build.

## Consequences

- Teams can gate pull requests on breaking changes today, with the CLI.
- The CLI binary grows by about 5 MB, from 55 to 61 MB.
- The server will use the same package for diffs between revisions and for a breaking-change check when publishing; those come with the spec page's diff view.
- oasdiff's messages are in English. Its localizer can supply other languages when the web app has them.

## Alternatives considered

- **libopenapi's `what-changed`**, with our own rules and paths. Rejected for now: we'd have to rebuild locations and the breaking rules, which is most of what oasdiff already does well. We can revisit it if the second parser causes real disagreements.
- **Running the oasdiff CLI as a separate binary.** Rejected. The server needs the checks in process, and one binary is easier to install.
