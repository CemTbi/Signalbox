# 0031. Postman and cURL import and export

- Status: Accepted
- Date: 2026-10-19
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) has Postman Collection v2.1 and environment files import into a project, collections export back to v2.1, and cURL commands import as single requests. Each import comes with a report of what will run differently and what was left out; v0.2 is done when real collections import with a clean report.

Postman's format has grown over the years: several fields have more than one shape, and collections lean on features Signalbox has only some of so far, such as auth inherited from folders, collection variables, scripts, and body modes.

## Decision

**Conversion is its own package** (`internal/modules/client/convert`), pure functions from files to Signalbox's shapes and back, with no database. It is tested against files, and the client module stores what it returns. Every reader returns a report of notes, each one of:
- **`differs`:** it will run differently, such as a script using something the sandbox lacks;
- **`omitted`:** it was left out, such as a file body;
- **`info`:** worth knowing but changes nothing, such as saved examples that weren't kept.

A report is clean when it has only `info` notes.

**Postman collections** (v2.1, and v2.0, whose fields differ in shape) import as a new collection in one transaction.
- **Folders and requests** keep their order, with names, descriptions, methods, headers (disabled ones stay, unticked) and URLs. Path variables such as `:id` are filled in from the URL's variables, as Postman does when sending.
- **Collection variables** are stored with the collection ([migration 00011](../../internal/platform/postgres/migrations/00011_collection_variables_and_scripts.sql)). A send names a `collectionId`, and its variables fill in `{{name}}` where the environment has none; the environment's win, as in Postman. They aren't secret: secrets belong in environments.
- **Auth** is copied onto each request from wherever it is defined, the request, its folders, or the collection, since requests can't inherit yet ([ADR-0030](0030-request-auth.md)). `basic`, `bearer`, `apikey` and `noauth` convert; other types are omitted and reported, and the request doesn't fall back to an ancestor's auth.
- **Bodies:**
  - `raw` is kept, with the Content-Type Postman sends for its language.
  - `urlencoded` is encoded, keeping `{{variables}}`. A variable in a field is reported, since its value isn't form-encoded after it's filled in.
  - `formdata` text fields become a multipart body, whose boundary comes from the content, so the same fields give the same body.
  - `graphql` becomes Postman's JSON body.
  - File fields and file bodies are omitted.
- **Scripts** are stored with the collection, folders and requests, and never run during an import. They are read for what the sandbox lacks: `pm.` members and `require`d modules outside [ADR-0017](0017-postman-scripts.md)'s list, `cheerio`, and `setNextRequest`. Until runs execute scripts, a note says they're kept but not run.
- **Also reported:**
  - dynamic variables Signalbox doesn't fill in, such as `{{$randomEmail}}`;
  - `protocolProfileBehavior` settings other than `disableBodyPruning`;
  - names and descriptions cut to Signalbox's limits;
  - saved examples, which aren't imported.
- **Limits:** a file of at most 25 MiB, 10,000 items, and folders 20 deep, as the RFC sets.

**Export** writes Postman Collection v2.1 with folders, requests, auth, collection variables and scripts. A request without auth exports as `noauth`, so Postman doesn't apply the collection's. Bodies export as `raw`. What Signalbox stores reads back unchanged, and the tests check that.

**Postman environments** merge into one of the project's environments. A variable with a name the environment has replaces it in place, others go at the end, and secrets the environment keeps are untouched unless replaced. Postman's `secret` type stays secret, so it needs the server's secret key ([ADR-0029](0029-environment-variables.md)). Disabled variables aren't imported, and invalid names are omitted.

**cURL** commands are split as a POSIX shell does: quotes, `$'…'`, backslash continuations. Chrome's Copy as cURL (cmd) output, with `^` escapes, is read too.
- **Converted options:** methods, headers, every `--data` form (`-G` puts data in the query), `--json`, text form fields, `-u` and `--oauth2-bearer` as auth, user agent, referer and cookies.
- **Ignored:** options that don't change the request, such as `-s` or `--compressed`.
- **Reported as `differs`:** options Signalbox can't follow, such as `--insecure` or `--proxy`, and unknown ones.
- **Omitted:** anything read from a file.

The result fills the editor and isn't stored until it's saved.

**HTTP:**
- **Import:** `POST /projects/{id}/collection-imports` and `POST /environments/{id}/variable-imports` take the Postman file as the JSON body. `?dryRun=true` returns the report without storing anything, and needs the same role as the import, so it reveals nothing more.
- **Export:** `GET /collections/{id}/export` returns the file as an attachment.
- **cURL:** `POST /curl-imports` returns the request and the report.

## Consequences

- Teams bring their Postman collections and environments, see exactly what won't carry over, and can take collections back out.
- Imported requests run with their collection's variables and auth from the Requests page. Their scripts run once scripts come to runs, which is the next step.
- Exported auth sits on each request rather than its folders, so a re-export of an imported collection repeats auth that was once inherited.

## Alternatives considered

- **Storing the Postman file as it is, and converting on run.** Rejected: collections are stored one row per item so they can be edited ([ADR-0028](0028-collections.md)), and a report needs the conversion anyway.
- **Running scripts in the sandbox to find problems.** Rejected: an import must never run code from the file. Reading the scripts finds what the spike found in practice.
- **Using Postman's own converter libraries in Node.** Rejected: the server is one Go binary ([ADR-0001](0001-single-binary-with-roles.md)), and the conversion is small.
