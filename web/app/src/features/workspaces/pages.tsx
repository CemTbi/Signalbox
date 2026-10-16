// SPDX-License-Identifier: Apache-2.0

import type { WorkspaceRole } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { projectsKey, projectsQuery, workspaceQuery, workspacesKey, workspacesQuery } from "../../queries";
import { ProjectForm, type ProjectValues } from "./ProjectForm";
import { WorkspaceForm, type WorkspaceValues } from "./WorkspaceForm";

const roleLabels: Record<WorkspaceRole, string> = {
  owner: "Owner",
  admin: "Admin",
  editor: "Editor",
  viewer: "Viewer",
};

export function RoleBadge({ role }: { role: WorkspaceRole }) {
  return <span className={`badge role-${role}`}>{roleLabels[role]}</span>;
}

export function WorkspacesPage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const list = useInfiniteQuery(workspacesQuery(api));
  const create = useMutation({
    mutationFn: (values: WorkspaceValues) => unwrap(api.POST("/api/v1/workspaces", { body: values })),
    onSuccess: async (ws) => {
      await queryClient.invalidateQueries({ queryKey: workspacesKey });
      await navigate({ to: "/workspaces/$workspaceId", params: { workspaceId: ws.id } });
    },
  });

  const workspaces = list.data?.pages.flatMap((p) => p.data) ?? [];
  const empty = list.isSuccess && workspaces.length === 0;

  return (
    <div className="stack">
      <PageHeader title="Workspaces" meta={<p className="lede">A workspace holds the projects, members and rules of one team.</p>} />
      <div className="split">
        <section aria-labelledby="your-workspaces" className="split-main">
          <h2 id="your-workspaces" className="section-title">
            Your workspaces
          </h2>
          {list.isPending ? <Loading label="Loading workspaces…" /> : null}
          {list.isError ? <LoadError message={describeError(list.error)} onRetry={() => void list.refetch()} /> : null}
          {empty ? (
            <div className="empty-state">
              <p className="empty-title">No workspaces yet</p>
              <p>Create one for your team to start adding projects.</p>
            </div>
          ) : null}
          {workspaces.length > 0 ? (
            <ul className="rows">
              {workspaces.map((ws) => (
                <li key={ws.id}>
                  <Link to="/workspaces/$workspaceId" params={{ workspaceId: ws.id }} className="row">
                    <span className="row-main">
                      <span className="row-title">{ws.name}</span>
                      <span className="row-sub mono">{ws.slug}</span>
                    </span>
                    <RoleBadge role={ws.role} />
                  </Link>
                </li>
              ))}
            </ul>
          ) : null}
          {list.hasNextPage ? (
            <button
              type="button"
              className="button secondary"
              disabled={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {list.isFetchingNextPage ? "Loading…" : "Show more"}
            </button>
          ) : null}
        </section>
        <section aria-labelledby="new-workspace" className="panel split-side">
          <h2 id="new-workspace" className="section-title">
            {empty ? "Create your first workspace" : "New workspace"}
          </h2>
          <WorkspaceForm onSubmit={(v) => create.mutate(v)} pending={create.isPending} error={create.error} />
        </section>
      </div>
    </div>
  );
}

const workspaceRoute = getRouteApi("/_app/workspaces/$workspaceId");

export function WorkspacePage() {
  const { workspaceId } = workspaceRoute.useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const workspace = useQuery(workspaceQuery(api, workspaceId));
  const projects = useInfiniteQuery(projectsQuery(api, workspaceId));
  const create = useMutation({
    mutationFn: (values: ProjectValues) =>
      unwrap(
        api.POST("/api/v1/workspaces/{workspaceId}/projects", {
          params: { path: { workspaceId } },
          body: values.description ? values : { name: values.name },
        }),
      ),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: projectsKey(workspaceId) });
      await navigate({ to: "/projects/$projectId", params: { projectId: project.id } });
    },
  });

  if (workspace.isPending) return <Loading label="Loading workspace…" />;
  if (workspace.isError) {
    return <LoadError message={describeError(workspace.error)} onRetry={() => void workspace.refetch()} />;
  }

  const ws = workspace.data;
  const list = projects.data?.pages.flatMap((p) => p.data) ?? [];
  const canCreate = ws.role !== "viewer";

  return (
    <div className="stack">
      <PageHeader
        title={ws.name}
        crumbs={[{ label: "Workspaces", to: "/workspaces" }, { label: ws.name }]}
        meta={
          <p className="page-meta-line">
            <span className="mono">{ws.slug}</span>
            <RoleBadge role={ws.role} />
          </p>
        }
      />
      <div className="split">
        <section aria-labelledby="projects" className="split-main">
          <h2 id="projects" className="section-title">
            Projects
          </h2>
          {projects.isPending ? <Loading label="Loading projects…" /> : null}
          {projects.isError ? (
            <LoadError message={describeError(projects.error)} onRetry={() => void projects.refetch()} />
          ) : null}
          {projects.isSuccess && list.length === 0 ? (
            <div className="empty-state">
              <p className="empty-title">No projects yet</p>
              <p>A project is one system, such as a shop or a payments platform, drawn as a canvas of its services.</p>
            </div>
          ) : null}
          {list.length > 0 ? (
            <ul className="rows">
              {list.map((p) => (
                <li key={p.id}>
                  <Link to="/projects/$projectId" params={{ projectId: p.id }} className="row">
                    <span className="row-main">
                      <span className="row-title">{p.name}</span>
                      {p.description ? <span className="row-sub">{p.description}</span> : null}
                    </span>
                    <span className="row-go" aria-hidden="true">
                      Open canvas →
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : null}
          {projects.hasNextPage ? (
            <button
              type="button"
              className="button secondary"
              disabled={projects.isFetchingNextPage}
              onClick={() => void projects.fetchNextPage()}
            >
              {projects.isFetchingNextPage ? "Loading…" : "Show more"}
            </button>
          ) : null}
        </section>
        <section aria-labelledby="new-project" className="panel split-side">
          <h2 id="new-project" className="section-title">
            New project
          </h2>
          {canCreate ? (
            <ProjectForm onSubmit={(v) => create.mutate(v)} pending={create.isPending} error={create.error} />
          ) : (
            <p className="muted">Viewers can't create projects. Ask a workspace owner, admin or editor.</p>
          )}
        </section>
      </div>
    </div>
  );
}
