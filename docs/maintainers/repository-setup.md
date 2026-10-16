# Repository setup

How the GitHub repository is configured, and what changes when it goes public. Settings live in GitHub, not in the code, so this page is the record of what they should be.

## GitHub apps

| App | Why | Status |
|---|---|---|
| [DCO](https://github.com/apps/dco) | Fails a pull request when a commit lacks a `Signed-off-by` line that matches its author. See [ADR-0007](../adr/0007-license-and-dco.md). | To install |
| [Renovate](https://github.com/apps/renovate) | Opens the weekly dependency pull requests configured in `renovate.json`. See [ADR-0008](../adr/0008-dependency-policy.md). | To install |
| [Claude](https://github.com/apps/claude) | Answers `@claude` in issues and pull requests. Needs the workflow and secret that `/install-github-app` in Claude Code sets up. | Installed |

## Branch rules for `main`

Create a ruleset under **Settings → Rules → Rulesets** that targets the default branch:

- Require a pull request before merging, with one approval once there is a second maintainer.
- Require these status checks: `Go`, `Web app`, `Container image and Compose smoke test` and `DCO`. Add `Analyze go` and `Analyze javascript-typescript` once CodeQL runs (see below).
- Require branches to be up to date before merging.
- Block force pushes and deletion.
- Allow squash merging only, so `main` reads as one Conventional Commit per pull request.

## Actions

- **Workflow permissions:** read-only by default. Each workflow asks for the extra permissions it needs.
- **Action pinning:** every action is pinned to a full commit SHA with its version in a comment. Renovate keeps them current. Turn on **Require actions to be pinned to a full-length commit SHA** so a new workflow can't skip this.
- **Minutes:** a private repository spends the account's included Actions minutes. One CI run takes about 15 minutes of runner time.

## Security settings

- **Dependabot alerts:** on. Renovate opens the fixes, so leave Dependabot security updates off to avoid duplicate pull requests.
- **Private vulnerability reporting:** on. [SECURITY.md](../../SECURITY.md) tells reporters to use it.
- **Secret scanning and push protection:** on. They are free for public repositories.

## Going public

Signalbox is open source and meant to be public. Before switching the repository's visibility:

1. Turn on private vulnerability reporting, secret scanning and push protection.
2. Add a weekly `schedule` trigger to `.github/workflows/codeql.yaml`. While the repository is private, CodeQL skips itself, because GitHub offers code scanning on private repositories only with GitHub Code Security.
3. Add the CodeQL checks to the branch ruleset.
4. Make the `signalbox-server` package on GitHub Container Registry public after the first release.

Cut the first release only after the repository is public. Releases are signed with Sigstore's public instance, and its transparency log records the repository name and workflow of every signature.
