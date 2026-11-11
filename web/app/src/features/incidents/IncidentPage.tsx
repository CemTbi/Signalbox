// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { incidentKey, incidentQuery, incidentsKey, monitorKey, projectQuery, workspaceQuery } from "../../queries";
import { describeEvent, formatDuration, incidentStatus, resolutionLabels } from "./model";

const route = getRouteApi("/_app/incidents/$incidentId");

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

/** One incident: what took the monitor down, what happened since, and Acknowledge and Resolve. */
export function IncidentPage() {
  const { incidentId } = route.useParams();
  const queryClient = useQueryClient();
  const incident = useQuery(incidentQuery(api, incidentId));
  const projectId = incident.data?.projectId ?? "";
  const project = useQuery({ ...projectQuery(api, projectId), enabled: incident.isSuccess });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: incidentKey(incidentId) }),
      queryClient.invalidateQueries({ queryKey: incidentsKey(projectId) }),
      incident.data?.monitorId ? queryClient.invalidateQueries({ queryKey: monitorKey(incident.data.monitorId) }) : Promise.resolve(),
    ]);
  };
  const acknowledge = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/incidents/{incidentId}/acknowledge", { params: { path: { incidentId } } })),
    onSuccess: refresh,
  });
  const resolve = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/incidents/{incidentId}/resolve", { params: { path: { incidentId } } })),
    onSuccess: refresh,
  });

  if (incident.isPending) return <Loading label="Loading incident…" />;
  if (incident.isError) return <LoadError message={describeError(incident.error)} onRetry={() => void incident.refetch()} />;
  const i = incident.data;
  const status = incidentStatus(i);
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";
  const open = i.state === "open";
  const actionError = acknowledge.error ?? resolve.error;
  return (
    <div className="stack">
      <PageHeader
        title={i.monitorName}
        documentTitle={`${i.monitorName} · Incidents`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: project.data?.workspaceId ?? "" } },
          { label: project.data?.name ?? "Project", to: "/projects/$projectId", params: { projectId } },
          { label: "Incidents", to: "/projects/$projectId/incidents", params: { projectId } },
          { label: i.monitorName },
        ]}
        meta={
          <p className="lede monitor-state" role="status" aria-label={`Incident: ${status.label}`}>
            <span className={`svc-mark ${status.mark}`} aria-hidden="true" />
            {status.label}
            <span className="field-hint">
              {open ? "Open for" : "Lasted"} {formatDuration(i.openedAt, i.resolvedAt)}
              {i.resolution ? ` · ${resolutionLabels[i.resolution]}` : ""}
            </span>
          </p>
        }
        actions={
          canEdit && open ? (
            <>
              {!i.acknowledgedAt ? (
                <button type="button" className="button primary" disabled={acknowledge.isPending} onClick={() => acknowledge.mutate()}>
                  {acknowledge.isPending ? "Acknowledging…" : "Acknowledge"}
                </button>
              ) : null}
              <button type="button" className="button secondary" disabled={resolve.isPending} onClick={() => resolve.mutate()}>
                {resolve.isPending ? "Resolving…" : "Resolve"}
              </button>
            </>
          ) : null
        }
      />
      {actionError ? (
        <p className="form-alert" role="alert">
          {describeError(actionError)}
        </p>
      ) : null}
      <section aria-label="What took it down" className="stack-tight">
        <dl className="monitor-facts">
          <div>
            <dt>Opened</dt>
            <dd>{dateTime.format(new Date(i.openedAt))}</dd>
          </div>
          {i.acknowledgedAt ? (
            <div>
              <dt>Acknowledged</dt>
              <dd>
                {dateTime.format(new Date(i.acknowledgedAt))}
                {i.acknowledgedBy ? ` by ${i.acknowledgedBy.name}` : ""}
              </dd>
            </div>
          ) : null}
          {i.resolvedAt ? (
            <div>
              <dt>Resolved</dt>
              <dd>
                {dateTime.format(new Date(i.resolvedAt))}
                {i.resolvedBy ? ` by ${i.resolvedBy.name}` : ""}
              </dd>
            </div>
          ) : null}
          <div>
            <dt>Monitor</dt>
            <dd>
              {i.monitorId ? (
                <Link to="/monitors/$monitorId" params={{ monitorId: i.monitorId }}>
                  {i.monitorName}
                </Link>
              ) : (
                `${i.monitorName} (deleted)`
              )}
            </dd>
          </div>
        </dl>
        {i.firstCheck.failureMessage ? (
          <p className="monitor-failure">
            {i.firstCheck.failureMessage}
            {i.firstCheck.traceId ? (
              <>
                {" "}
                <Link to="/projects/$projectId/traces/$traceId" params={{ projectId, traceId: i.firstCheck.traceId }}>
                  Open trace
                </Link>
              </>
            ) : null}
            {i.firstCheck.runId ? (
              <>
                {" "}
                <Link to="/runs/$runId" params={{ runId: i.firstCheck.runId }}>
                  Open the run
                </Link>
              </>
            ) : null}
          </p>
        ) : null}
        {open && !i.acknowledgedAt ? <p className="field-hint">Acknowledge it to say you&apos;re on it, and to stop repeat notifications.</p> : null}
      </section>
      <section aria-labelledby="timeline" className="stack-tight">
        <h2 id="timeline" className="section-title">
          Timeline
        </h2>
        <ol className="incident-timeline">
          {i.timeline.map((e, n) => (
            <li key={`${e.at}-${n}`}>
              <time dateTime={e.at}>{dateTime.format(new Date(e.at))}</time>
              <span>
                {describeEvent(e)}
                {e.actor ? <span className="field-hint"> · {e.actor.name}</span> : null}
              </span>
              {e.kind === "state" && e.detail ? <span className="field-hint incident-detail">{e.detail}</span> : null}
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}
