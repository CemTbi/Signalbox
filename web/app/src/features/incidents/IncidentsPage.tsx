// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useMemo } from "react";
import { api, describeError } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { incidentsQuery, projectQuery, workspaceQuery } from "../../queries";
import { formatDuration, incidentStatus } from "./model";

const route = getRouteApi("/_app/projects/$projectId/incidents");

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" });

/** A project's incidents: the open ones first, then the newest. */
export function IncidentsPage() {
  const { projectId } = route.useParams();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const incidents = useInfiniteQuery(incidentsQuery(api, projectId));
  const list = useMemo(() => incidents.data?.pages.flatMap((p) => p.data) ?? [], [incidents.data]);

  if (project.isPending) return <Loading label="Loading incidents…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  const p = project.data;
  return (
    <div className="stack">
      <PageHeader
        title="Incidents"
        documentTitle={`Incidents · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId: p.id } },
          { label: "Incidents" },
        ]}
        meta={<p className="lede">A monitor going down opens an incident, which resolves when it&apos;s up again or someone resolves it.</p>}
      />
      {incidents.isPending ? <Loading label="Loading incidents…" /> : null}
      {incidents.isError ? <LoadError message={describeError(incidents.error)} onRetry={() => void incidents.refetch()} /> : null}
      {incidents.isSuccess && list.length === 0 ? (
        <div className="empty-state">
          <p className="empty-title">No incidents</p>
          <p>
            None of this project&apos;s{" "}
            <Link to="/projects/$projectId/monitors" params={{ projectId }}>
              monitors
            </Link>{" "}
            has gone down.
          </p>
        </div>
      ) : null}
      {list.length > 0 ? (
        <table className="traces" aria-label="Incidents">
          <thead>
            <tr>
              <th scope="col">Monitor</th>
              <th scope="col">Status</th>
              <th scope="col">Opened</th>
              <th scope="col">Lasted</th>
              <th scope="col">What took it down</th>
            </tr>
          </thead>
          <tbody>
            {list.map((i) => {
              const status = incidentStatus(i);
              return (
                <tr key={i.id}>
                  <td>
                    <Link to="/incidents/$incidentId" params={{ incidentId: i.id }}>
                      {i.monitorName}
                    </Link>
                  </td>
                  <td>
                    <span className="monitor-state">
                      <span className={`svc-mark ${status.mark}`} aria-hidden="true" />
                      {status.label}
                    </span>
                  </td>
                  <td>{dateTime.format(new Date(i.openedAt))}</td>
                  <td>{formatDuration(i.openedAt, i.resolvedAt)}</td>
                  <td>{i.firstCheck.failureMessage ?? ""}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
      {incidents.hasNextPage ? (
        <div>
          <button type="button" className="button secondary" disabled={incidents.isFetchingNextPage} onClick={() => void incidents.fetchNextPage()}>
            {incidents.isFetchingNextPage ? "Loading…" : "Show more"}
          </button>
        </div>
      ) : null}
    </div>
  );
}
