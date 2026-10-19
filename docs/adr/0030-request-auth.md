# 0030. Request auth

- Status: Accepted
- Date: 2026-10-19
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) gives requests auth: `none`, `apikey`, `basic`, `bearer` and `oauth2` client credentials, inherited from the folder, then the collection. Postman collections, which import next, use auth on most requests, often with `{{variables}}` in the credentials and inherited from a folder.

Auth can't be turned into headers when a request is saved or imported. Basic auth encodes the username and password together, so `{{password}}` has to be filled in before the encoding, and a secret must never be encoded into a saved header.

## Decision

**A request carries its auth** (`internal/modules/client/auth.go`): saved requests keep it beside their headers, and `POST /send` takes it.
- **Types:**
  - `basic` sends `Authorization: Basic` with the encoded username and password.
  - `bearer` sends `Authorization: Bearer` with the token.
  - `apikey` sends a value in a named header, or as a query parameter, escaped.
  - No auth is `none`, or no auth at all.
- **Applied last:** variables are filled in first, in the auth's fields too, so a password or token can be a secret variable. Auth replaces any header of the same name the request has, so a stale `Authorization` header can't win.
- **History** keeps the auth's type, username and API key name, never its credentials. It also masks every credential the auth sent, whether it came from a secret variable or was typed in:
  - the encoded basic value and the password;
  - the token;
  - the API key, as typed and as escaped in the query.

  These values are masked wherever they appear, the response included, as secrets are ([ADR-0029](0029-environment-variables.md)).
- **Reopening from history** brings back the auth type without its credentials, and the editor says to type them again.

**Inheritance and OAuth 2.0 come later.** Folders and collections have no auth of their own yet, so the Postman import resolves inheritance into each request. `oauth2` client credentials needs a token request and a cache, which come with scripts and runs.

## Consequences

- Saved requests and sends authenticate with basic, bearer or an API key, with credentials kept as secrets.
- A credential typed directly into an auth field is still masked in history. A short credential that also appears elsewhere in a response shows as `•••` there too; that's preferred to showing it.
- The Postman import can convert `basic`, `bearer`, `apikey` and `noauth` without changing how requests run, and reports the other types.

## Alternatives considered

- **Turning auth into headers on save.** Rejected: see Context. It also loses the auth when the collection is exported.
- **A template function such as `{{$base64 …}}`.** Rejected: it invents syntax Postman doesn't have, and still puts credentials in headers that history shows.
