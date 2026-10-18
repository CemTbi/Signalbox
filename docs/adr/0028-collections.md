# 0028. Collections of saved requests

- Status: Accepted
- Date: 2026-10-18
- Deciders: project lead

## Context

[RFC 0001](../rfcs/0001-api-client-and-tests.md) stores collections one row per folder or request, with fractional ranks, so that people editing different requests never conflict. Building it settled the details the RFC left open:
- how ranks work;
- who may delete what;
- how edits and moves meet;
- what lists carry.

## Decision

**Ranks are fractional indexes** (`internal/modules/client/rank.go`), after David Greenspan's "Implementing Fractional Indexing":
- **Format:** base-62 digits in ASCII order, compared byte by byte, so PostgreSQL orders them with `COLLATE "C"`. No rank ends in `0`, so there's always room between two.
- **Moving:** an item moves by taking a rank between its new neighbours, and nothing else changes.
- **Appending:** the common case bumps the first digit with room, so a folder's ranks grow by a character every 37 or so appends instead of every 5.
- **Uniqueness:** a unique index on collection, parent and rank stops two items sharing a place.

**Changes to a collection's order are serialized** by locking the collection's row for the transaction, which also makes the item count and depth checks exact. Edits to different items in a collection never conflict otherwise. Each item has its own ETag. `If-Match` is optional on items, so a quick rename from the tree doesn't need a fresh copy, and required behaviour can come later if people lose work to it.

**The tree stays a tree:**
- **Structure:** a folder can't move inside itself, an item's parent must be a folder in the same collection, folders nest at most 20 deep, and a collection holds at most 10,000 items.
- **Deletes:** deleting a folder deletes its contents, through the foreign key.

**Roles:**
- **Editors** create and change collections, folders and requests, and delete folders and requests.
- **Deleting a whole collection** takes an owner or admin, as deleting a spec does.

**Lists are light.** `GET …/items` returns every item of a collection in tree order, with each request's method and URL only, which is enough to draw the tree. Opening a request fetches it whole.

**A saved request** holds the method, URL, headers (each one on or off, as in Postman) and body. Variables in them stay as `{{name}}` text until environments resolve them in the next step. Sizes match the send API's, so anything sendable is saveable.

## Consequences

- People save requests in collections and folders, and reopen, rename and delete them. Saving an open request with a stale ETag is refused, and says so.
- Moving items works through the API; the web app's drag and drop comes later.
- Importing Postman collections fits this shape: folders and requests in order, with headers that can be off.

## Alternatives considered

- **One JSON document per collection.** Rejected in the RFC: any two edits to a collection would conflict.
- **Integer positions.** Rejected: a move renumbers every sibling after it, which conflicts with concurrent moves.
- **Lexorank-style buckets and rebalancing.** Rejected as unneeded: fractional indexes without rebalancing stay short for any real folder, and the format allows a rebalance later if one is ever needed.
