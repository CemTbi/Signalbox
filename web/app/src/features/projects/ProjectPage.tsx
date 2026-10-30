// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { api, describeError } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { projectQuery, workspaceQuery } from "../../queries";
import { ServicesSection } from "../services/ServicesSection";

// The canvas library is large, so it loads only when a canvas is shown.
const Canvas = lazy(() => import("./Canvas"));

const projectRoute = getRouteApi("/_app/projects/$projectId");

export function ProjectPage() {
  const { projectId } = projectRoute.useParams();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({
    ...workspaceQuery(api, project.data?.workspaceId ?? ""),
    enabled: project.isSuccess,
  });

  if (project.isPending) return <Loading label="Loading project…" />;
  if (project.isError) {
    return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  }

  const p = project.data;
  const wsName = workspace.data?.name ?? "Workspace";
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";

  return (
    <div className="stack project">
      <PageHeader
        title={p.name}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: wsName, to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name },
        ]}
        meta={p.description ? <p className="lede">{p.description}</p> : null}
        actions={
          <>
            <Link to="/projects/$projectId/ingest-keys" params={{ projectId: p.id }} className="button ghost">
              Ingest keys
            </Link>
            <Link to="/projects/$projectId/requests" params={{ projectId: p.id }} className="button secondary">
              Requests
            </Link>
          </>
        }
      />
      <section className="canvas-frame" aria-label={`Canvas of ${p.name}`}>
        <Suspense fallback={<Loading label="Loading canvas…" />}>
          <Canvas projectId={p.id} canEdit={canEdit} />
        </Suspense>
      </section>
      <ServicesSection projectId={p.id} canEdit={canEdit} />
    </div>
  );
}
