# 0010. First-run setup, the public URL and request origins

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

A new installation has no accounts. Somebody has to create the first one, and on a server reachable from a network, the first visitor might not be the person who installed it.

Browsers only keep `Secure` cookies on HTTPS sites and on `localhost`. Self-hosters often try new software over plain HTTP on a LAN address first, where the `__Host-` session cookie from [ADR-0004](0004-authentication-and-sessions.md) would be dropped and nobody could sign in.

The CSRF defense in ADR-0004 compares a request's `Origin` header with the installation's own origin. Behind reverse proxies and port mappings, the server can't reliably work out its own origin from a request.

## Decision

**The public URL.** `SIGNALBOX_PUBLIC_URL` is the address people open Signalbox at, such as `https://signalbox.example.com`, served from the root of its host. It defaults to `http://localhost:8080`. It decides the origin that unsafe requests must come from, the session cookie's name and flags, and the links the server writes, such as the setup page's address.

**The session cookie.** Over HTTPS the cookie is `__Host-sbx_session` with `Secure`. Over plain HTTP it is `sbx_session` without `Secure`, and the server warns at startup when the public URL isn't a loopback address, because passwords and cookies then travel unencrypted.

**Request origins.** Every unsafe request must carry an `Origin` header equal to the public URL's origin; a request without one passes only with `Sec-Fetch-Site: same-origin`. Requests made with a session must also carry the session's CSRF token in `X-CSRF-Token`. Signing in and completing setup, where no session exists yet, check the origin only. `GET /api/v1/sessions/current` returns the token, because scripts can't read the cookie.

**First-run setup.** While no account exists, the server writes a setup code to its log at startup: 16 characters of Crockford base32, about 80 bits, grouped in fours. Whoever completes the setup page with that code creates the first account, an administrator, and is signed in. Operators can choose the code with `SIGNALBOX_SETUP_CODE` instead, which automation and installations with several api replicas need. Setup runs in one transaction under an advisory lock, so two people can't both win, and afterwards the setup endpoint answers `409`.

**Rate limits.** Setup allows 5 attempts at once and then 5 a minute per client address. Sign-in allows 10 at once, then 10 a minute per client address, and 5 at once, then 5 a minute per account. `X-Forwarded-For` is believed only from addresses in `SIGNALBOX_TRUSTED_PROXIES`.

## Consequences

- An installation reachable by others is safe from the moment it starts: claiming it needs the log.
- Operators must set `SIGNALBOX_PUBLIC_URL` to the address people use. If they don't, sign-in fails with a message that names the variable.
- Plain HTTP works for trials on a private network, with a warning, instead of failing in a way that is hard to diagnose.
- Rate limits live in each process. With several api replicas, each enforces its own, so the effective limit grows with the replica count. Shared limits will move to NATS key-value with the other coordination state from [ADR-0002](0002-data-stores.md).

## Alternatives considered

- **The first visitor becomes the administrator.** Rejected. Anyone who reaches a new installation first could claim it.
- **A default administrator password, changed at first sign-in.** Rejected. Default credentials get left in place.
- **The first administrator's password in an environment variable.** Rejected. It leaves a long-lived password in deployment files, where a one-time code expires on use.
- **Always `__Host-sbx_session`, so HTTPS or localhost only.** Rejected. Trials over plain HTTP on a LAN would fail without a clear reason.
- **Working out the origin from `Host` and `X-Forwarded-*` headers.** Rejected. Proxies differ in what they send, and a wrong guess silently weakens the CSRF check.
