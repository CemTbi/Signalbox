# 0029. Environment variables and secrets

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) gives each environment variables and secrets, which fill in `{{name}}` when a request is sent, and asks for secrets encrypted at rest with envelope encryption. Building it settled the details the RFC left open:
- how keys are kept;
- how the variables of an environment are edited;
- how `{{name}}` is filled in;
- what history keeps of a request that used a secret.

## Decision

**Envelope encryption** (`internal/platform/secretbox`):
- **Key-encryption key:** the installation's key, from `SIGNALBOX_SECRET_KEY` or the file in `SIGNALBOX_SECRET_KEY_FILE`: 32 random bytes in base64. It never reaches the database.
- **Data keys:** each workspace gets a random data key the first time it stores a secret. The key-encryption key wraps it, and `workspace_keys` keeps it with the first 8 bytes of the key-encryption key's SHA-256, which tells a changed key from damaged data.
- **Cipher:** AES-256-GCM with random 96-bit nonces, in both layers. Each ciphertext is bound by additional data to where it belongs: a data key to its workspace, a secret to its environment and name. A ciphertext copied to another row doesn't decrypt.
- **Rotation:** changing the key-encryption key means re-wrapping the data keys only. The command for it comes with key rotation; until then, a changed key gives `secret-key-changed`, which says to restore the old key.
- **Without a key**, environments keep plain variables, and storing a secret fails with `secrets-unavailable`, which says how to set one. The server logs at startup that secrets are off.

**An environment's variables are one set.** `GET` and `PUT /api/v1/environments/{id}/variables` read and replace all of them, in order, with one ETag (`environment_variable_sets.version`). People edit an environment's variables together, as a table, and a set is small (at most 500 variables of 64 KiB each). Per-variable resources would make reordering and renaming several requests that can half-fail. `If-Match` is optional, as on collection items.

**Secrets are write-only.** Reads return a secret's name without its value. A `PUT` that sends a secret without `value` keeps the stored one, so the editor can save a table without knowing the secrets in it. Anyone in the workspace can see plain values; editors and above change them.

**Filling in `{{name}}`** happens in the client module, before the runner:
- **Where:** the URL, header names and values, and the body.
- **Names:** a variable's name may hold anything but spaces and braces, and doesn't start with `$`, which marks the built-ins `{{$guid}}`, `{{$timestamp}}`, `{{$isoTimestamp}}` and `{{$randomInt}}`, as Postman has them. Spaces inside the braces are ignored.
- **Nesting:** a plain value may name other variables, up to 8 deep. A secret is used as it is, so a secret that happens to contain `{{` stays intact.
- **Unknown names** are sent as they are, and the response lists them in `unresolved`. Failing the send would block requests that use `{{` on purpose, such as a template sent to an API.
- **Size and work:** the filled-in request is bounded at 12 MiB, so variables that name each other twice over can't grow it without end. The bytes read while filling in are bounded at 64 MiB, so a chain that repeats names many times without growing the request, such as one ending in an empty value, can't keep the server busy. Past either bound the send fails with `request-too-large`.
- **Which environment:** the send names an `environmentId`, which must be in the project. The web app remembers each person's choice per project in their browser.

**History never holds a secret.** What history keeps is the request as sent, with every secret's value replaced by `•••` wherever it appears: the URL, headers, the body, a failure's message, which can name the host, and the response's headers and body too, since APIs often echo what they were sent. The masking happens before the body is cut at its size limit, so a cut can't leave part of a secret behind. The response the sender gets back is whole, since it is theirs.

## Consequences

- Requests use `{{baseUrl}}` and secrets per environment, and switching the environment sends the same request elsewhere.
- Installations need to set and back up `SIGNALBOX_SECRET_KEY` to use secrets. The compose file has a place for it, and the e2e stack has a fixed test key.
- A history entry that used a secret can't be resent as it is from history; the request opened from it shows `•••` where the secret was, and says so.
- Creating, renaming and deleting environments are still to come; projects have their development, staging and production environments.
- Postman environment import maps onto this shape: an ordered list of keys and values, each one a secret or not.

## Alternatives considered

- **Encrypting with the key-encryption key directly.** Rejected: rotating it would mean re-encrypting every secret, and one key would cover every workspace.
- **A KMS or Vault.** Not yet: the key-encryption key is the seam for one, and a self-hosted platform must run without either.
- **Keeping the template in history.** Rejected for now: the filled-in request is what was sent and what the response answers, and masking keeps it safe. The template stays in the saved request.
- **Failing on unknown names.** Rejected; see above.
