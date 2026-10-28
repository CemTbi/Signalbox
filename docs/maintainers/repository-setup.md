# Repository setup

How the GitHub repository is configured, and what changes when it goes public. Settings live in GitHub, not in the code, so this page is the record of what they should be.

The repository is public, on GitHub's Free plan, since 2026-10-28.

## GitHub apps

| App | Why | Status |
|---|---|---|
| [DCO](https://github.com/apps/dco) | Fails a pull request when a commit lacks a `Signed-off-by` line that matches its author. See [ADR-0007](../adr/0007-license-and-dco.md). | Installed |
| [Renovate](https://github.com/apps/renovate) | Opens the weekly dependency pull requests configured in `renovate.json`. See [ADR-0008](../adr/0008-dependency-policy.md). | Installed |
| [Claude](https://github.com/apps/claude) | Reviews pull requests when they are ready, and answers `@claude` in issues and pull requests. The workflows are `.github/workflows/claude-code-review.yml` and `claude.yml`; they use the `CLAUDE_CODE_OAUTH_TOKEN` secret. | Installed |

## Branch rules for `main`

Until the ruleset below exists, `main` changes only through pull requests by convention, and CI reports on every one.

Create a ruleset under **Settings → Rules → Rulesets** that targets the default branch:

- Require a pull request before merging. Require no approvals while the project has one maintainer, because GitHub doesn't let the maintainer approve pull requests opened from their own account.
- Require these status checks: `Go`, `Web app`, `Image, Compose stack and end-to-end tests` and `DCO`. Add `Analyze go` and `Analyze javascript-typescript` once CodeQL runs (see below).
- Require branches to be up to date before merging.
- Block force pushes and deletion.
- Allow squash merging only, so `main` reads as one Conventional Commit per pull request.

Also turn on **Automatically delete head branches** under **Settings → General**, so merged branches don't pile up.

## Actions

- **Workflow permissions:** read-only by default. Each workflow asks for the extra permissions it needs.
- **Action pinning:** every action is pinned to a full commit SHA with its version in a comment. Renovate keeps them current. Turn on **Require actions to be pinned to a full-length commit SHA** so a new workflow can't skip this.
- **Minutes:** public repositories don't use minutes on standard runners. While the repository was private, the Free plan's 2,000 minutes a month ran out on 2026-10-28, with a full CI run taking 12 to 40 minutes of runner time. These keep runs short:
  - draft pull requests skip the image and end-to-end job; it runs when the pull request is marked ready for review, and on `main`
  - the Claude review runs when a pull request is opened or marked ready, not on every push; comment `@claude review` to ask again
  - pushes to a pull request cancel its CI run that is still going

## Security settings

- **Dependabot alerts:** on. Renovate opens the fixes, so leave Dependabot security updates off to avoid duplicate pull requests.
- **Private vulnerability reporting:** on once the repository is public. [SECURITY.md](../../SECURITY.md) tells reporters to use it.
- **Secret scanning and push protection:** on once the repository is public, where they are free.

## Going public

Signalbox is open source, and went public on 2026-10-28, which also lifted the Free plan's limits on branch rules, CodeQL and Actions minutes. The checklist, and where it stands:

1. Done: private vulnerability reporting, secret scanning and push protection are on.
2. Done: `.github/workflows/codeql.yaml` runs weekly as well as on every change.
3. To do: create the branch ruleset above, turn on squash merging only and automatic deletion of head branches, then add the CodeQL checks to the ruleset once they have run on `main`.
4. Before the first release: choose a contact address for code of conduct reports and put it in [CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md). GitHub has no private messages, so a GitHub handle isn't enough.
5. After the first release: make the `signalbox-server` package on GitHub Container Registry public.

Cut the first release only after the repository is public. Releases are signed with Sigstore's public instance, and its transparency log records the repository name and workflow of every signature.
