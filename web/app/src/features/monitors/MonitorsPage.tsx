// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, monitorsKey, monitorsQuery, monitorStatsQuery, projectQuery, servicesQuery, workspaceQuery } from "../../queries";
import { formatMs } from "../canvas/ServiceNode";
import { chooseEnvironment } from "../projects/placement";
import { MonitorForm } from "./MonitorForm";
import { emptyMonitor, formatInterval, formatUptime, kindLabels, type MonitorDraft, stateLabels, stateMarks, toCreate } from "./model";
import { keepFreshToken } from "./tokens";

const route = getRouteApi("/_app/projects/$projectId/monitors");

const timeFormat = new Intl.DateTimeFormat(undefined, { timeStyle: "medium" });

/** A project's monitors: their state, uptime and response time, and a form to add one. */
export function MonitorsPage() {
  const { projectId } = route.useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const environments = useQuery(environmentsQuery(api, projectId));
  const servicePages = useInfiniteQuery(servicesQuery(api, projectId));
  const monitors = useInfiniteQuery(monitorsQuery(api, projectId));
  const stats = useQuery(monitorStatsQuery(api, projectId));
  const list = useMemo(() => monitors.data?.pages.flatMap((p) => p.data) ?? [], [monitors.data]);
  const services = useMemo(() => servicePages.data?.pages.flatMap((p) => p.data) ?? [], [servicePages.data]);
  const statsOf = useMemo(() => new Map((stats.data?.data ?? []).map((s) => [s.monitorId, s])), [stats.data]);
  const envList = environments.data?.data ?? [];
  const nameOfEnv = new Map(envList.map((e) => [e.id, e.name]));

  const [draft, setDraft] = useState<MonitorDraft | undefined>(undefined);
  const form = draft ?? emptyMonitor(chooseEnvironment(envList, ""));
  const create = useMutation({
    mutationFn: (d: MonitorDraft) => unwrap(api.POST("/api/v1/projects/{projectId}/monitors", { params: { path: { projectId } }, body: toCreate(d) })),
    onSuccess: async (m) => {
      if (m.heartbeatToken) keepFreshToken(m.id, m.heartbeatToken);
      await queryClient.invalidateQueries({ queryKey: monitorsKey(projectId) });
      await navigate({ to: "/monitors/$monitorId", params: { monitorId: m.id } });
    },
  });

  if (project.isPending || environments.isPending) return <Loading label="Loading monitors…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  if (environments.isError) return <LoadError message={describeError(environments.error)} onRetry={() => void environments.refetch()} />;

  const p = project.data;
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";
  return (
    <div className="stack">
      <PageHeader
        title="Monitors"
        documentTitle={`Monitors · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId: p.id } },
          { label: "Monitors" },
        ]}
        meta={<p className="lede">Checks of this project&apos;s APIs on a schedule. A monitor with a service puts its health on the canvas.</p>}
      />
      {monitors.isPending ? <Loading label="Loading monitors…" /> : null}
      {monitors.isError ? <LoadError message={describeError(monitors.error)} onRetry={() => void monitors.refetch()} /> : null}
      {monitors.isSuccess && list.length === 0 ? (
        <div className="empty-state">
          <p className="empty-title">No monitors yet</p>
          <p>A monitor sends a request or runs a collection on a schedule, or waits for a job&apos;s heartbeat, and says when something isn&apos;t right.</p>
        </div>
      ) : null}
      {list.length > 0 ? (
        <table className="traces" aria-label="Monitors">
          <thead>
            <tr>
              <th scope="col">Monitor</th>
              <th scope="col">State</th>
              <th scope="col">Environment</th>
              <th scope="col">Last check</th>
              <th scope="col">Uptime, 24 h</th>
              <th scope="col">p95, 24 h</th>
            </tr>
          </thead>
          <tbody>
            {list.map((m) => {
              const s = statsOf.get(m.id);
              return (
                <tr key={m.id}>
                  <td>
                    <Link to="/monitors/$monitorId" params={{ monitorId: m.id }}>
                      {m.name}
                    </Link>
                    <span className="row-sub">
                      {kindLabels[m.kind]} · {formatInterval(m.intervalSeconds)}
                    </span>
                  </td>
                  <td>
                    <span className="monitor-state">
                      <span className={`svc-mark ${stateMarks[m.state]}`} aria-hidden="true" />
                      {stateLabels[m.state]}
                    </span>
                  </td>
                  <td>{nameOfEnv.get(m.environmentId) ?? ""}</td>
                  <td>{m.lastCheck ? timeFormat.format(new Date(m.lastCheck.at)) : "Not yet"}</td>
                  <td>{s ? formatUptime(s.uptime) : "–"}</td>
                  <td>{s && m.kind !== "heartbeat" ? formatMs(s.p95Ms) : "–"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
      {stats.isError ? (
        <p className="form-alert" role="alert">
          Uptime couldn&apos;t load: {describeError(stats.error)}
        </p>
      ) : null}
      {canEdit ? (
        <section aria-labelledby="new-monitor" className="panel">
          <h2 id="new-monitor" className="section-title">
            Add a monitor
          </h2>
          <MonitorForm
            draft={form}
            onChange={setDraft}
            onSubmit={() => create.mutate(form)}
            projectId={projectId}
            environments={envList}
            services={services}
            pending={create.isPending}
            error={create.error}
            submitLabel="Add monitor"
            label="New monitor"
          />
        </section>
      ) : null}
    </div>
  );
}
