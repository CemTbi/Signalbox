# 0012. Invitation links instead of email

- Status: Accepted
- Date: 2026-10-16
- Deciders: project lead

## Context

A workspace needs more than its creator. Most products invite people by email, but many self-hosted installations have no mail server configured, and Signalbox's budget rules out a paid mail service for the project's own demo. Invitations also create accounts, so a leaked invitation must not let a stranger in.

## Decision

- **Links, not email.** Owners and admins create an invitation for an email address and a role. Signalbox returns a link once; the inviter sends it however they like. Email delivery can be added later as an option without changing this flow.
- **The token is a secret.** Each link carries 256 random bits. Only the token's SHA-256 hash is stored, the link works once and expires after 7 days, and inviting the same address again replaces the old link.
- **The token never reaches the server's URL handling.** The link is `https://<public URL>/invite#<token>`. Browsers don't send the part after `#`, so the token can't appear in access logs, proxy logs or `Referer` headers. The invitation page reads it and sends it in request bodies to `POST /api/v1/invitations/lookup`, `/accept` and `/sign-up`.
- **Bound to an email address.** Only an account with the invited address can accept. Someone without an account creates one from the link, with that address fixed; someone with an account signs in first. A different signed-in account is told to sign out.
- **Roles.** Only owners invite owners. Owners and admins manage members, admins can't change or remove owners, and a workspace always keeps at least one owner: changes to a workspace's members take turns under a row lock, so two owners can't demote each other at once.

## Consequences

- Invitations work on every installation, with or without mail.
- The inviter sees the link once and must pass it on themselves. Losing it means inviting again, which replaces the old link.
- Anyone who obtains a link before it is used can see the workspace's name and the invited address, but can only join with an account for that address.

## Alternatives considered

- **Email invitations only.** Rejected. They would fail on installations without mail, which is a common first setup.
- **Admins create accounts and hand out passwords.** Rejected. Admins would know other people's passwords, and passwords travel in chat.
- **The token in the URL path or query.** Rejected. It would end up in logs and `Referer` headers.
