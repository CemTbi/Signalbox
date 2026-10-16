-- Invitations to join a workspace. Owned by internal/modules/workspaces.

-- +goose Up
CREATE TABLE workspace_invitations (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    email        text NOT NULL,
    role         text NOT NULL,
    -- SHA-256 of the token in the invitation link. The link is shown once
    -- and the token itself is never stored.
    token_hash   bytea NOT NULL,
    invited_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    CONSTRAINT workspace_invitations_token_hash_key UNIQUE (token_hash),
    CONSTRAINT workspace_invitations_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT workspace_invitations_role CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    CONSTRAINT workspace_invitations_email_length CHECK (char_length(email) BETWEEN 3 AND 254)
);

-- One open invitation per address and workspace; inviting again replaces it.
CREATE UNIQUE INDEX workspace_invitations_workspace_email_key ON workspace_invitations (workspace_id, lower(email));
CREATE INDEX workspace_invitations_expires_at_idx ON workspace_invitations (expires_at);

-- Lists a workspace's members in user order, and finds its owners.
CREATE INDEX workspace_members_workspace_role_idx ON workspace_members (workspace_id, role);

-- +goose Down
DROP INDEX workspace_members_workspace_role_idx;
DROP TABLE workspace_invitations;
