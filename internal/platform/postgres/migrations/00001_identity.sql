-- Accounts and browser sessions. Owned by internal/modules/identity.
-- See docs/adr/0004-authentication-and-sessions.md.

-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL,
    name          text NOT NULL,
    -- An Argon2id hash in PHC string format. NULL for accounts that sign in
    -- only through single sign-on.
    password_hash text,
    admin         boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_length CHECK (char_length(email) BETWEEN 3 AND 254),
    CONSTRAINT users_name_length CHECK (char_length(name) BETWEEN 1 AND 100)
);

-- Email addresses are unique regardless of case.
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE sessions (
    -- SHA-256 of the cookie value. The value itself is never stored, so a
    -- copy of this table can't be used to sign in.
    token_hash   bytea PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    client_ip    inet,
    user_agent   text,
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
