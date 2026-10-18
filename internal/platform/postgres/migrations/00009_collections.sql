-- Collections of saved requests, one row per folder or request, so people
-- editing different requests never conflict. Owned by
-- internal/modules/client. See docs/rfcs/0001-api-client-and-tests.md.

-- +goose Up
CREATE TABLE collections (
    id          uuid PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    version     bigint NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT collections_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT collections_description_length CHECK (char_length(description) <= 2000)
);

CREATE INDEX collections_project_id_idx ON collections (project_id, id);
CREATE INDEX collections_created_by_idx ON collections (created_by);

CREATE TABLE collection_items (
    id            uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    -- The folder the item is in; null at the top of the collection.
    parent_id     uuid REFERENCES collection_items (id) ON DELETE CASCADE,
    -- The item's place among its siblings: a fractional index that sorts
    -- byte by byte.
    rank          text COLLATE "C" NOT NULL,
    kind          text NOT NULL,
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    -- The request, for kind request: method, URL, headers and body.
    request       jsonb,
    version       bigint NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT collection_items_kind CHECK (kind IN ('folder', 'request')),
    CONSTRAINT collection_items_request CHECK ((kind = 'request') = (request IS NOT NULL)),
    CONSTRAINT collection_items_name_length CHECK (char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT collection_items_description_length CHECK (char_length(description) <= 10000),
    CONSTRAINT collection_items_rank_format CHECK (rank ~ '^[0-9A-Za-z]*[1-9A-Za-z]$' AND char_length(rank) <= 200),
    CONSTRAINT collection_items_request_size CHECK (pg_column_size(request) <= 11534336)
);

-- Siblings in order. Two items can't share a place.
CREATE UNIQUE INDEX collection_items_order_key ON collection_items (collection_id, coalesce(parent_id, '00000000-0000-0000-0000-000000000000'), rank);
CREATE INDEX collection_items_parent_id_idx ON collection_items (parent_id);

-- +goose Down
DROP TABLE collection_items;
DROP TABLE collections;
