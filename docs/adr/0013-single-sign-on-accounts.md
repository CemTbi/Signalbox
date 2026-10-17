# 0013. Single sign-on accounts through OpenID Connect

- Status: Accepted
- Date: 2026-10-17
- Deciders: project lead

## Context

[ADR-0004](0004-authentication-and-sessions.md) promises single sign-on for everyone and rules that an account is never matched to an SSO identity by email alone. The walking skeleton needs the first, smallest version of that promise: people sign in with the identity provider their organization already runs, such as Okta, Microsoft Entra ID, Google, Keycloak or Authentik, without anyone creating their accounts by hand.

Signing in through a provider raises questions a password doesn't. Which account belongs to the person who just came back from the provider? Who may get an account at all? What stops a forged or replayed answer from the provider? A mistake in any of these lets someone take over another person's account.

## Decision

**One OpenID Connect provider per installation, set by the operator.** `SIGNALBOX_OIDC_ISSUER`, `SIGNALBOX_OIDC_CLIENT_ID` and `SIGNALBOX_OIDC_CLIENT_SECRET` (or `SIGNALBOX_OIDC_CLIENT_SECRET_FILE`) turn it on; there is no flag for the secret, so it can't show up in a process list. The provider is registered with the redirect URL `<public URL>/api/v1/sso/callback`, which the server logs at startup. `SIGNALBOX_OIDC_NAME` names the provider on the sign-in page. Discovery happens at the first sign-in, so an unreachable provider doesn't stop the server; password sign-in keeps working.

**The authorization code flow, hardened.** Signalbox uses PKCE with S256, a random `state` and a random `nonce`, and verifies the ID token's signature, issuer, audience, expiry and nonce. The state is stored only as a SHA-256 hash, works once and expires after 10 minutes. It is bound to the browser that started the sign-in by a second random value in a short-lived `HttpOnly`, `SameSite=Lax` cookie (`__Host-sbx_sso` over HTTPS). A callback that arrives in another browser fails, which stops an attacker from signing a victim into the attacker's account.

**An identity is the issuer and the subject.** Accounts are found by the pair (`iss`, `sub`) in `user_identities`, never by email address. Email addresses can be reassigned or edited at many providers; the subject can't.

**The first sign-in creates an account** with the name and email address the provider sends, and no password. Signalbox creates it only if:

- the provider vouches for the email address with `email_verified`. `SIGNALBOX_OIDC_TRUST_UNVERIFIED_EMAIL` accepts addresses from providers that never send the claim and are known to verify addresses themselves;
- the email domain is allowed, when `SIGNALBOX_OIDC_ALLOWED_DOMAINS` lists domains. The startup log points out an empty list, because with a public provider such as Google anyone with an account there could then sign in;
- no account has the email address yet. Otherwise the sign-in is refused and the person is told to use their password. Linking an existing account to an identity needs that account's owner to be signed in, and comes in a later change;
- first-run setup is complete. The first account, the administrator, comes from the setup code ([ADR-0010](0010-first-run-setup.md)), so whoever reaches a new installation first can't claim it through the provider.

An account from single sign-on has no workspaces until someone invites it, like any other new account.

**Coming back to the right page.** The sign-in carries a path on this site to return to. Anything else, such as another host, becomes `/`. The part of an address after `#`, such as an invitation token ([ADR-0012](0012-invitation-links.md)), stays in the browser's session storage during the trip, so it never reaches the server or the provider.

## Consequences

- Teams with an identity provider can start using Signalbox without creating accounts. Someone removed at the provider can't sign in again, but sessions they already have last until they expire, after 12 hours idle or 7 days at most.
- Someone with a password account can't use single sign-on until linking exists, and the sign-in page says so.
- Provider claims that change later, such as a new name or email address, don't change the Signalbox account yet.
- The end-to-end tests sign in through Dex, a real OpenID Connect provider, in the Compose stack, so the browser flow is tested on every pull request.

## Alternatives considered

- **Find accounts by email address.** Rejected. Anyone who can make a provider assert an address, for example by creating an account with an unverified address at a public provider, would take over the matching Signalbox account.
- **Plain OAuth 2.0 sign-in, such as "Sign in with GitHub".** Rejected for now. Without ID tokens, each provider needs its own code to identify people and check their email addresses. A bridge such as Dex already offers GitHub sign-in through OpenID Connect.
- **Several providers and SAML 2.0 now.** Deferred. One provider covers the walking skeleton; ADR-0004 still plans SAML, and settings in the UI rather than the environment.
- **Group and role mapping from provider claims.** Deferred. Roles come from workspace membership and invitations until there is a design for keeping them in sync.
