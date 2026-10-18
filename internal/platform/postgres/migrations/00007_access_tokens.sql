-- Personal access tokens, which act as their person for the CLI, the agent
-- and scripts. Owned by internal/modules/identity.
-- See docs/rfcs/0001-api-client-and-tests.md.

-- +goose Up
CREATE TABLE access_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    -- SHA-256 of the whole token, sbx_pat_ included; the token isn't stored.
    token_hash   bytea NOT NULL,
    -- The token's last four characters, to tell tokens apart.
    hint         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    CONSTRAINT access_tokens_token_hash_key UNIQUE (token_hash),
    CONSTRAINT access_tokens_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT access_tokens_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT access_tokens_hint_length CHECK (char_length(hint) = 4),
    CONSTRAINT access_tokens_expiry CHECK (expires_at > created_at)
);

-- Lists a person's tokens; IDs are UUIDv7, so ID order is creation order.
CREATE INDEX access_tokens_user_id_idx ON access_tokens (user_id, id);
CREATE INDEX access_tokens_expires_at_idx ON access_tokens (expires_at);

-- +goose Down
DROP TABLE access_tokens;
