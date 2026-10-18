# 0026. Personal access tokens

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

The CLI's server mode, the local agent and people's own scripts need to call the API without a browser ([RFC 0001](../rfcs/0001-api-client-and-tests.md)). Until now the API accepted only browser sessions, with a cookie and a CSRF token, and answered `Authorization: Bearer` with "API tokens aren't available yet".

## Decision

**A personal access token acts as its person.** It's sent as `Authorization: Bearer sbx_pat_…` and can do what its person can, through the same permission checks.
- **No CSRF or origin check.** A token rides in a header that browsers never add on their own, so a page on another site can't make a browser send it.
- **Token management needs a browser session.** Listing, creating and revoking tokens, and the session endpoints, answer a token with `403 browser-session-required`. A leaked token can't mint itself a successor.

**Tokens are secrets shown once.**
- **The secret:** `sbx_pat_` followed by 256 random bits in base64url. The prefix lets secret scanners, such as GitHub's, and people recognise a leaked token.
- **What's stored:** only the SHA-256 of the whole token, as for sessions ([ADR-0004](0004-authentication-and-sessions.md)). With 256 random bits, a fast hash is enough.
- **The last four characters** are kept as a hint, to tell tokens apart.
- **The response that carries the secret** says `Cache-Control: no-store`.

**Every token expires.** It lasts 90 days by default, and 1 to 366 days as chosen. Expired tokens stay listed for 30 days, so people see what stopped working, and are then deleted. A person may have 50.

**Use is visible.** Each token records when it was last used, to the minute; the write happens at most once a minute per token. The access log names the user and the token's ID for every request made with one, never its value. Revoking a token deletes it, and the next request with it fails.

## Consequences

- The CLI and the agent can authenticate, and people can script the API.
- A token is as powerful as its person. Narrower scopes, and service-account tokens owned by a workspace for CI (`sbx_sat_…`), come before 1.0.
- Rotating a token means creating a new one and revoking the old. Both can work at once, so rotation needs no downtime.

## Alternatives considered

- **OAuth 2.0 device flow for the CLI.** It's better for people at a terminal, and planned with SSO-only installations in mind. But CI and the agent still need a long-lived secret, which is what tokens are.
- **Hashing tokens with Argon2.** Rejected: a token has 256 random bits, so a slow hash adds cost to every request and no protection.
- **Letting tokens manage tokens.** Rejected, so that revoking a leaked token is enough.
