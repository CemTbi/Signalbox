# Repository setup

How the GitHub repository is configured, and what changes when it goes public. Settings live in GitHub, not in the code, so this page is the record of what they should be.

The repository is private on GitHub's Free plan for now. Some settings below need a public repository or a paid plan; they are marked.

## GitHub apps

| App | Why | Status |
|---|---|---|
| [DCO](https://github.com/apps/dco) | Fails a pull request when a commit lacks a `Signed-off-by` line that matches its author. See [ADR-0007](../adr/0007-license-and-dco.md). | Installed |
| [Renovate](https://github.com/apps/renovate) | Opens the weekly dependency pull requests configured in `renovate.json`. See [ADR-0008](../adr/0008-dependency-policy.md). | Installed |
| [Claude](https://github.com/apps/claude) | Reviews pull requests when they are ready, and answers `@claude` in issues and pull requests. The workflows are `.github/workflows/claude-code-review.yml` and `claude.yml`; they use the `CLAUDE_CODE_OAUTH_TOKEN` secret. | Installed |

## Branch rules for `main`

Branch rules need a public repository or GitHub Pro. Until then, `main` changes only through pull requests by convention, and CI reports on every one.

When they become available, create a ruleset under **Settings → Rules → Rulesets** that targets the default branch:

- Require a pull request before merging. Require no approvals while the project has one maintainer, because GitHub doesn't let the maintainer approve pull requests opened from their own account.
- Require these status checks: `Go`, `Web app`, `Image, Compose stack and end-to-end tests` and `DCO`. Add `Analyze go` and `Analyze javascript-typescript` once CodeQL runs (see below).
- Require branches to be up to date before merging.
- Block force pushes and deletion.
- Allow squash merging only, so `main` reads as one Conventional Commit per pull request.

Also turn on **Automatically delete head branches** under **Settings → General**, so merged branches don't pile up.

## Actions

- **Workflow permissions:** read-only by default. Each workflow asks for the extra permissions it needs.
- **Action pinning:** every action is pinned to a full commit SHA with its version in a comment. Renovate keeps them current. Turn on **Require actions to be pinned to a full-length commit SHA** so a new workflow can't skip this.
- **Minutes:** a private repository on the Free plan gets 2,000 Actions minutes a month. A full CI run takes about 12 minutes of runner time with warm caches and about 30 without. To stay within the budget:
  - draft pull requests skip the image and end-to-end job; it runs when the pull request is marked ready for review, and on `main`
  - the Claude review runs when a pull request is opened or marked ready, not on every push; comment `@claude review` to ask again
  - pushes to a pull request cancel its CI run that is still going

  Public repositories don't use minutes on standard runners.

## Security settings

- **Dependabot alerts:** on. Renovate opens the fixes, so leave Dependabot security updates off to avoid duplicate pull requests.
- **Private vulnerability reporting:** on once the repository is public. [SECURITY.md](../../SECURITY.md) tells reporters to use it.
- **Secret scanning and push protection:** on once the repository is public, where they are free.

## Going public

Signalbox is open source and meant to be public. Going public also lifts the Free plan's limits above: branch rules, CodeQL and Actions minutes. Before switching the repository's visibility:

1. Choose a contact address for code of conduct reports and put it in [CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md). GitHub has no private messages, so a GitHub handle isn't enough.
2. Turn on private vulnerability reporting, secret scanning and push protection.
3. Add a weekly `schedule` trigger to `.github/workflows/codeql.yaml`. While the repository is private, CodeQL skips itself, because GitHub offers code scanning on private repositories only with GitHub Code Security.
4. Create the branch ruleset above, with the CodeQL checks.
5. Make the `signalbox-server` package on GitHub Container Registry public after the first release.

Cut the first release only after the repository is public. Releases are signed with Sigstore's public instance, and its transparency log records the repository name and workflow of every signature.
