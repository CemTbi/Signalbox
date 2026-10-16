# 0004. Authentication, sessions and API tokens

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

Signalbox is self-hosted by organizations that already have an identity provider, and by small teams that don't. It stores API secrets and can send requests into private networks, so a stolen session is serious. People use it from a browser, the CLI, CI pipelines and other tools.

## Decision

**Accounts**

- Local accounts with passwords hashed by Argon2id (64 MiB memory, 3 iterations, parallelism 1, 16-byte salt). Parameters are stored with each hash so they can be raised later.
- Second factors: passkeys (WebAuthn) and TOTP. Admins can require a second factor.
- Single sign-on through OIDC and SAML 2.0, included for everyone; no feature sits behind a paid tier. An instance can disable local passwords once SSO works.
- Accounts are never matched to an SSO identity by email alone. Linking needs a signed-in session or admin action.

**Browser sessions**

- Server-side sessions in PostgreSQL. The cookie holds only an opaque 256-bit random ID: `__Host-sbx_session`, `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`.
- The ID changes at sign-in and on privilege changes. Sessions expire after 12 hours idle and 7 days absolute; admins can shorten both.
- Unsafe methods (POST, PUT, PATCH, DELETE) require a matching `Origin` header and an `X-CSRF-Token` header tied to the session.
- Signing out deletes the session on the server.

**API tokens**

- Personal access tokens and service-account tokens, scoped to workspaces and permissions, with an expiry date.
- Format `sbx_pat_<random>` and `sbx_sat_<random>`, so secret scanners can recognize leaked tokens.
- Only a SHA-256 hash of each token is stored. Tokens have 256 bits of entropy, so a fast hash is safe. The plain token is shown once.
- Sent as `Authorization: Bearer <token>`.

**Authorization**

- Roles at workspace level (owner, admin, editor, viewer), with project-level overrides.
- One authorization layer checks every request. PostgreSQL row-level security on workspace-scoped tables is a second line of defense.
- Security-relevant actions go to the audit log: sign-ins, failed sign-ins, token creation, role changes, SSO configuration changes.

**Rate limits** on sign-in, password reset and token endpoints, per account and per client address.

## Consequences

- Companies connect their existing identity provider on day one.
- Server-side sessions cost a database read per request. A short in-process cache keeps that cheap, and sessions can be revoked instantly.
- Until sign-in lands in the walking skeleton, the API is unauthenticated and must only run on trusted networks. The release notes for any build without authentication say so.

## Alternatives considered

- **JWTs in cookies as sessions.** Rejected. They can't be revoked without a server-side list, which removes their advantage.
- **An embedded identity server such as Keycloak or Ory Kratos.** Rejected. It adds a service to every installation for features that well-tested Go libraries cover.
- **SSO only in a commercial edition.** Rejected. Signalbox isn't sold, and security features shouldn't cost extra.
