# 0036. CI templates for test suites

- Status: Accepted
- Date: 2026-10-28
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) gates pull requests on test suites. It asks for a GitHub Action that installs the CLI and runs a suite, with annotations and a job summary, and for a GitLab CI include with the JUnit report wired into merge requests. `signalbox test run` ([ADR-0034](0034-cli-test-run.md)) already runs suites offline and on a server, and writes JUnit reports.

## Decision

**The GitHub Action lives in this repository**, at `actions/test`, as a composite action: `uses: CemTbi/Signalbox/actions/test@v0.2.0`. It releases with the CLI, so a tag pins both. A separate repository would need its own releases kept in step.

**It installs a released CLI.** It downloads the archive for the runner's system and architecture from the repository's GitHub releases with `gh`, which works for private repositories with the job's token. It checks the archive against the release's `checksums.txt` before extracting it. The checksums file is signed with Sigstore ([ADR-0008](0008-dependency-policy.md)); verifying that signature needs cosign on the runner, which the action leaves to teams that want it. `signalbox-path` uses a binary you provide instead, which this repository's own CI does to test the action with the CLI it just built.

**Annotations and the job summary come from the CLI**, through a `github` reporter:
- each failed request's failure, script errors and failed tests become `::error` annotations, at most 50;
- a Markdown summary, with the totals and the failed requests, is appended to `$GITHUB_STEP_SUMMARY`.

Text from the run, such as test names, is escaped for workflow commands and for Markdown and HTML, so a collection can't inject either. The action passes every input to the CLI through environment variables, never by expanding them into the script.

**Failing.** The action records the CLI's exit code as an output, uploads the reports whatever happened, then fails the step when the code isn't 0. `continue-on-error` and the output let a workflow act on a failure.

**The GitLab template** is `ci/gitlab/signalbox-test.yml`, a hidden job `.signalbox-test` to extend and configure with variables:
- it runs on Alpine and installs the CLI the same way, with `curl` and `sha256sum`;
- it keeps the reports as artifacts whatever happened, and declares the JUnit file as the job's `junit` report, so merge requests show failed tests.

`SIGNALBOX_SERVER` and `SIGNALBOX_TOKEN` are read by the CLI, so they can stay masked CI/CD variables.

## Consequences

- Teams gate pull requests and merge requests on a Postman export or a stored suite with a few lines.
- Neither template works until a release exists: they install released CLIs. The repository's own CI tests the action on every change, with a CLI built from the change.
- The GitLab template is checked locally (its scripts run in Alpine) but not in CI, which runs on GitHub.

## Alternatives considered

- **A Docker container action** with the CLI inside. Rejected: it runs only on Linux runners, and needs an image published and pulled for every run.
- **`go install` in the action.** Rejected: it needs Go and a build on every run, and builds from source instead of the signed release.
- **Annotations from the JUnit file**, with a third-party action. Rejected: another dependency to pin and trust, and the CLI knows more, such as script errors.
