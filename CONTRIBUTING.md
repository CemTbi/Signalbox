# Contributing to Signalbox

Thank you for helping. This guide covers how to set up a development environment, how changes get from an idea to `main`, and what a finished change includes.

## Ground rules

- Be kind. The [code of conduct](CODE_OF_CONDUCT.md) applies everywhere the project lives.
- Discuss big changes first. Anything that adds a module, changes the public API or the data model, or adds a dependency to a shipped artifact starts as an [RFC](docs/rfcs/README.md). Small fixes can go straight to a pull request.
- Report security problems privately, as described in [SECURITY.md](SECURITY.md).

## Development setup

You need:

- Go 1.27 or newer
- Node.js 22 or newer, with npm, for the web app
- Docker, for the Compose stack and integration tests
- `make` (optional). On Windows, `scoop install make` or Git Bash works.

Then:

```sh
git clone https://github.com/CemTbi/Signalbox.git signalbox && cd signalbox
make test          # Go tests with the race detector
make web           # build the web app into the server
make build         # build bin/signalbox and bin/signalbox-server
make run           # start the server on :8080 with readable logs
```

Without `make`, run the commands from the [Makefile](Makefile) directly.

## Making a change

1. Find or open an issue that describes the problem. For larger work, open an RFC.
2. Create a short-lived branch from `main`.
3. Write the change with its tests. Keep pull requests small enough to review in one sitting.
4. Commit with a sign-off and a [Conventional Commits](https://www.conventionalcommits.org/) message:

   ```sh
   git commit -s -m "feat(specengine): report the position of duplicate operationIds"
   ```

   Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`. The release notes are generated from these messages.
5. Open a pull request and fill in the template. CI must be green before review.

### The DCO sign-off

Signalbox uses the [Developer Certificate of Origin](https://developercertificate.org/) instead of a contributor license agreement. The `-s` flag adds a `Signed-off-by` line, which certifies that you wrote the change or have the right to submit it under the project's license. A bot checks every commit in a pull request. If you forgot, fix it with `git rebase --signoff main` and force-push your branch.

### Reviews

A code owner reviews every pull request. Changes to authentication, the request runner, the script sandbox or secrets handling need two approving reviews. Reviewers look for correctness, tests, clear names and the definition of done below.

## Definition of done

A change is done when:

- it has tests at the right level, and they pass
- user documentation and the API reference describe the new behavior
- new code paths emit useful logs, metrics and traces
- UI changes work with a keyboard and a screen reader
- the changelog has an entry for anything a user would notice
- database migrations follow expand-then-contract, so old and new versions run side by side during a rolling upgrade

## Code style

**Go.** Format with `gofmt`. Wrap errors with context (`fmt.Errorf("load spec %s: %w", id, err)`). Pass `context.Context` as the first argument to anything that does I/O. Log with `log/slog`, never with `fmt.Println`. Every source file starts with `// SPDX-License-Identifier: Apache-2.0`.

**TypeScript.** Strict mode, no `any` without a comment that explains why. Format with Prettier and lint with ESLint (`npm --prefix web run lint`).

**API changes.** Change `api/openapi.yaml` first, in the same pull request as the code. CI lints it with `signalbox lint` and flags breaking changes.

## Tests

| Command | What it runs |
|---|---|
| `make test` | Every Go test with the race detector, including performance budgets |
| `make test-short` | Go tests without the slower performance budgets |
| `make web-check` | Web app lint, type check and unit tests |

Integration tests that need PostgreSQL or ClickHouse use Testcontainers and skip themselves when Docker isn't available.

## Releases

Maintainers cut releases from `main` by pushing a `vX.Y.Z` tag. CI builds signed binaries and images, attaches SBOMs and provenance, and publishes the release notes.
