-- Workspaces, their members, projects and environments. Owned by
-- internal/modules/workspaces.

-- +goose Up
CREATE TABLE workspaces (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    slug       text NOT NULL,
    -- Increments on every change. The API sends it as the ETag.
    version    bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workspaces_slug_key UNIQUE (slug),
    CONSTRAINT workspaces_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 63),
    CONSTRAINT workspaces_name_length CHECK (char_length(name) BETWEEN 1 AND 100)
);

CREATE TABLE workspace_members (
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    CONSTRAINT workspace_members_role CHECK (role IN ('owner', 'admin', 'editor', 'viewer'))
);

-- Lists a user's workspaces in ID order.
CREATE INDEX workspace_members_user_id_idx ON workspace_members (user_id, workspace_id);

CREATE TABLE projects (
    id           uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    version      bigint NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT projects_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT projects_description_length CHECK (char_length(description) <= 1000)
);

-- Lists a workspace's projects in ID order.
CREATE INDEX projects_workspace_id_idx ON projects (workspace_id, id);

CREATE TABLE environments (
    id         uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    slug       text NOT NULL,
    kind       text NOT NULL,
    -- The environment's place in the environment switcher, from 0.
    position   integer NOT NULL,
    version    bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT environments_project_slug_key UNIQUE (project_id, slug),
    CONSTRAINT environments_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 63),
    CONSTRAINT environments_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT environments_kind CHECK (kind IN ('development', 'staging', 'production', 'other')),
    CONSTRAINT environments_position CHECK (position >= 0)
);

-- +goose Down
DROP TABLE environments;
DROP TABLE projects;
DROP TABLE workspace_members;
DROP TABLE workspaces;
