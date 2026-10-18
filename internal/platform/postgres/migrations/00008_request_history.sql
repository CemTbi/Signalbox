-- What each person sent from the API client: the request, with credential
-- headers redacted, and the response. Kept 30 days, and at most 1,000
-- entries per person and project. Owned by internal/modules/client.
-- See docs/rfcs/0001-api-client-and-tests.md.

-- +goose Up
CREATE TABLE request_history (
    id            uuid PRIMARY KEY,
    project_id    uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    method        text NOT NULL,
    url           text NOT NULL,
    -- Null when the request got no response.
    status        integer,
    -- The request's headers and body, the response's metadata, the
    -- failure if any, and the timings.
    record        jsonb NOT NULL,
    -- The first megabyte of the response body.
    response_body bytea,
    duration_us   bigint NOT NULL,
    sent_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT request_history_url_length CHECK (char_length(url) <= 8192),
    CONSTRAINT request_history_body_size CHECK (octet_length(response_body) <= 1048576)
);

-- A person's history in a project, newest first; IDs are UUIDv7.
CREATE INDEX request_history_owner_idx ON request_history (project_id, user_id, id DESC);
CREATE INDEX request_history_user_id_idx ON request_history (user_id);
CREATE INDEX request_history_sent_at_idx ON request_history (sent_at);

-- +goose Down
DROP TABLE request_history;
