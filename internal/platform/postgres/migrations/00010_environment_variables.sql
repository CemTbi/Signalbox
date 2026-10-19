-- Environment variables and their encrypted secrets, and each workspace's
-- data key. Owned by internal/modules/client. See
-- docs/rfcs/0001-api-client-and-tests.md and docs/adr/0029-environment-variables.md.

-- +goose Up

-- A workspace's data key, encrypted with the installation's key-encryption
-- key, which key_id names.
CREATE TABLE workspace_keys (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    wrapped_key  bytea NOT NULL,
    key_id       text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- An environment's variables change together; the set's version is their
-- ETag.
CREATE TABLE environment_variable_sets (
    environment_id uuid PRIMARY KEY REFERENCES environments (id) ON DELETE CASCADE,
    version        bigint NOT NULL DEFAULT 1,
    updated_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE environment_variables (
    environment_id uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    key            text NOT NULL,
    -- A plain value; null for a secret.
    value          text,
    -- A secret's value, encrypted with the workspace's data key; null for
    -- a plain value.
    secret_value   bytea,
    PRIMARY KEY (environment_id, key),
    CONSTRAINT environment_variables_key_format CHECK (char_length(key) BETWEEN 1 AND 200 AND key !~ '[{}\s]' AND left(key, 1) <> '$'),
    CONSTRAINT environment_variables_value_length CHECK (char_length(value) <= 65536),
    CONSTRAINT environment_variables_one_value CHECK ((value IS NULL) <> (secret_value IS NULL))
);

-- +goose Down
DROP TABLE environment_variables;
DROP TABLE environment_variable_sets;
DROP TABLE workspace_keys;
