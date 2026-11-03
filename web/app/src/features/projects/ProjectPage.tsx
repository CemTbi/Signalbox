// SPDX-License-Identifier: Apache-2.0

import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { lazy, Suspense, useId, useState } from "react";
import { api, describeError } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, projectQuery, workspaceQuery } from "../../queries";
import { ServicesSection } from "../services/ServicesSection";
import { DeclaredDependencies, SeenServices } from "./CanvasPanels";
import { chooseEnvironment, rememberCanvasEnvironment, rememberedCanvasEnvironment } from "./placement";

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

  const environments = useQuery(environmentsQuery(api, projectId));
  const [chosen, setChosen] = useState(() => rememberedCanvasEnvironment(projectId));
  const environmentId = chooseEnvironment(environments.data?.data ?? [], chosen);
  const environmentField = useId();

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
            <Link to="/projects/$projectId/traces" params={{ projectId: p.id }} className="button secondary">
              Traces
            </Link>
            <Link to="/projects/$projectId/requests" params={{ projectId: p.id }} className="button secondary">
              Requests
            </Link>
          </>
        }
      />
      {environments.data && environments.data.data.length > 0 ? (
        <div className="canvas-controls">
          <div className="field">
            <label htmlFor={environmentField}>Environment</label>
            <select
              id={environmentField}
              value={environmentId}
              onChange={(e) => {
                setChosen(e.target.value);
                rememberCanvasEnvironment(projectId, e.target.value);
              }}
            >
              {environments.data.data.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </select>
          </div>
        </div>
      ) : null}
      <section className="canvas-frame" aria-label={`Canvas of ${p.name}`}>
        <Suspense fallback={<Loading label="Loading canvas…" />}>
          <Canvas projectId={p.id} environmentId={environmentId} canEdit={canEdit} />
        </Suspense>
      </section>
      <SeenServices projectId={p.id} environmentId={environmentId} canEdit={canEdit} />
      <ServicesSection projectId={p.id} canEdit={canEdit} />
      <DeclaredDependencies projectId={p.id} canEdit={canEdit} />
    </div>
  );
}
