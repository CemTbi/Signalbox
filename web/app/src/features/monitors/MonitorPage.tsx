// SPDX-License-Identifier: Apache-2.0

import type { Check, Monitor } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { api, describeError, unwrap, unwrapTagged } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { checksKey, checksQuery, environmentsQuery, monitorKey, monitorQuery, monitorsKey, monitorStatsQuery, projectQuery, servicesQuery, workspaceQuery } from "../../queries";
import { formatMs } from "../canvas/ServiceNode";
import { MonitorForm } from "./MonitorForm";
import { chartBars, formatInterval, formatUptime, fromMonitor, type MonitorDraft, outcomeLabels, outcomeMarks, stateLabels, stateMarks, toUpdate } from "./model";

const route = getRouteApi("/_app/monitors/$monitorId");

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "long" });

/** One monitor: what it found lately, why it's in the state it is, and its settings. */
export function MonitorPage() {
  const { monitorId } = route.useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const tagged = useQuery(monitorQuery(api, monitorId));
  const monitor = tagged.data?.data;
  const projectId = monitor?.projectId ?? "";
  const project = useQuery({ ...projectQuery(api, projectId), enabled: monitor !== undefined });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const environments = useQuery({ ...environmentsQuery(api, projectId), enabled: monitor !== undefined });
  const checks = useQuery(checksQuery(api, monitorId));
  const stats = useQuery({ ...monitorStatsQuery(api, projectId), enabled: monitor !== undefined });

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: monitorKey(monitorId) }),
      queryClient.invalidateQueries({ queryKey: checksKey(monitorId) }),
      queryClient.invalidateQueries({ queryKey: monitorsKey(projectId) }),
    ]);
  };
  const checkNow = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/monitors/{monitorId}/check", { params: { path: { monitorId } } })),
    onSuccess: refresh,
  });
  const pause = useMutation({
    mutationFn: (paused: boolean) =>
      unwrapTagged(api.PATCH("/api/v1/monitors/{monitorId}", { params: { path: { monitorId }, header: { "If-Match": tagged.data?.etag ?? "" } }, body: { paused } })),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE("/api/v1/monitors/{monitorId}", { params: { path: { monitorId }, header: { "If-Match": tagged.data?.etag ?? "" } } })),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: monitorsKey(projectId) });
      await navigate({ to: "/projects/$projectId/monitors", params: { projectId } });
    },
  });

  if (tagged.isPending) return <Loading label="Loading monitor…" />;
  if (tagged.isError) return <LoadError message={describeError(tagged.error)} onRetry={() => void tagged.refetch()} />;
  const m = tagged.data.data;
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";
  const env = environments.data?.data.find((e) => e.id === m.environmentId)?.name ?? "";
  const dayStats = stats.data?.data.find((s) => s.monitorId === m.id);
  const paused = m.state === "paused";
  const actionError = checkNow.error ?? pause.error ?? remove.error;

  return (
    <div className="stack">
      <PageHeader
        title={m.name}
        documentTitle={`${m.name} · Monitors`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: project.data?.workspaceId ?? "" } },
          { label: project.data?.name ?? "Project", to: "/projects/$projectId", params: { projectId } },
          { label: "Monitors", to: "/projects/$projectId/monitors", params: { projectId } },
          { label: m.name },
        ]}
        meta={
          <p className="lede monitor-state" role="status" aria-label={`State: ${stateLabels[m.state]}`}>
            <span className={`svc-mark ${stateMarks[m.state]}`} aria-hidden="true" />
            {stateLabels[m.state]}
            <span className="field-hint">
              {env} · {m.request.method} <span className="mono">{m.request.url}</span> · {formatInterval(m.intervalSeconds)}
            </span>
          </p>
        }
        actions={
          canEdit ? (
            <>
              <button type="button" className="button primary" disabled={checkNow.isPending} onClick={() => checkNow.mutate()}>
                {checkNow.isPending ? "Checking…" : "Check now"}
              </button>
              <button type="button" className="button secondary" disabled={pause.isPending} onClick={() => pause.mutate(!paused)}>
                {paused ? "Resume" : "Pause"}
              </button>
              <button type="button" className="button ghost" disabled={remove.isPending} onClick={() => remove.mutate()}>
                Delete
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
      <LastCheck monitor={m} uptime={dayStats ? formatUptime(dayStats.uptime) : undefined} p95={dayStats ? formatMs(dayStats.p95Ms) : undefined} />
      <section aria-labelledby="results" className="stack-tight">
        <h2 id="results" className="section-title">
          Recent checks
        </h2>
        {checks.isPending ? <Loading label="Loading checks…" /> : null}
        {checks.isError ? <LoadError message={describeError(checks.error)} onRetry={() => void checks.refetch()} /> : null}
        {checks.data && checks.data.data.length === 0 ? <p className="field-hint">No checks in the last day yet.</p> : null}
        {checks.data && checks.data.data.length > 0 ? (
          <>
            <ResponseTimes checks={checks.data.data} />
            <CheckTable checks={checks.data.data} projectId={projectId} />
          </>
        ) : null}
      </section>
      {canEdit ? <Settings key={tagged.data.etag} monitor={m} etag={tagged.data.etag} onSaved={refresh} /> : null}
    </div>
  );
}

function LastCheck({ monitor: m, uptime, p95 }: { monitor: Monitor; uptime?: string; p95?: string }) {
  const last = m.lastCheck;
  return (
    <section aria-label="Latest" className="stack-tight">
      <dl className="monitor-facts">
        <div>
          <dt>Last check</dt>
          <dd>{last ? dateTime.format(new Date(last.at)) : "Not yet"}</dd>
        </div>
        <div>
          <dt>Next check</dt>
          <dd>{m.state === "paused" ? "Paused" : dateTime.format(new Date(m.nextCheckAt))}</dd>
        </div>
        <div>
          <dt>Uptime, 24 h</dt>
          <dd>{uptime ?? "–"}</dd>
        </div>
        <div>
          <dt>p95, 24 h</dt>
          <dd>{p95 ?? "–"}</dd>
        </div>
        {m.certificateExpiresAt ? (
          <div>
            <dt>Certificate expires</dt>
            <dd>{date.format(new Date(m.certificateExpiresAt))}</dd>
          </div>
        ) : null}
      </dl>
      {m.consecutiveFailures > 0 && m.state !== "down" ? (
        <p className="field-hint">
          {m.consecutiveFailures} of {m.failuresToConfirm} failures in a row. It&apos;s being confirmed before the monitor goes down.
        </p>
      ) : null}
      {last?.failureMessage ? <p className="monitor-failure">{last.failureMessage}</p> : null}
      {last && last.components.length > 0 ? (
        <ul className="monitor-components" aria-label="Health components">
          {last.components.map((c) => (
            <li key={c.name}>
              <span className={`svc-mark ${c.status}`} aria-hidden="true" />
              <span className="mono">{c.name}</span> <span className="field-hint">{c.status === "ok" ? "OK" : c.status === "warn" ? "Warning" : "Down"}</span>
              {c.detail ? <span className="field-hint"> · {c.detail}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

/**
 * Response times, one bar per check, oldest on the left. A check that isn't
 * a plain pass has its health shape over its bar, so failures don't rest on
 * color; the table below holds the same results for those who can't see it.
 */
function ResponseTimes({ checks }: { checks: Check[] }) {
  const bars = useMemo(() => chartBars(checks.slice(0, 100)), [checks]);
  const [hover, setHover] = useState<Check | undefined>(undefined);
  const slowest = Math.max(0, ...bars.map((b) => (b.check.outcome === "skipped" ? 0 : b.check.durationMs)));
  const kinds = new Set(bars.map((b) => b.check.outcome));
  return (
    <figure className="check-chart">
      <figcaption className="check-chart-head">
        <span>Response time, slowest {formatMs(slowest)}</span>
        <span className="check-chart-read" aria-hidden="true">
          {hover ? `${dateTime.format(new Date(hover.at))} · ${hover.outcome === "skipped" ? "skipped" : formatMs(hover.durationMs)} · ${outcomeLabels[hover.outcome]}` : ""}
        </span>
      </figcaption>
      <div className="check-bars" role="img" aria-label={`Response times of the last ${bars.length} checks. The table below lists them.`} onMouseLeave={() => setHover(undefined)}>
        {bars.map(({ check, height }) => (
          <div key={check.at} className="check-column" onMouseEnter={() => setHover(check)}>
            {check.outcome !== "passed" ? <span className={`svc-mark ${outcomeMarks[check.outcome]}`} /> : null}
            <span className={`check-bar ${check.outcome}`} style={{ height: `${height * 100}%` }} />
          </div>
        ))}
      </div>
      {kinds.size > 1 ? (
        <ul className="check-legend">
          {[...kinds].map((k) => (
            <li key={k}>
              <span className={`svc-mark ${outcomeMarks[k]}`} aria-hidden="true" />
              {outcomeLabels[k]}
            </li>
          ))}
        </ul>
      ) : null}
    </figure>
  );
}

function CheckTable({ checks, projectId }: { checks: Check[]; projectId: string }) {
  return (
    <div className="table-scroll">
      <table className="traces" aria-label="Checks">
        <thead>
          <tr>
            <th scope="col">Time</th>
            <th scope="col">Outcome</th>
            <th scope="col">Status</th>
            <th scope="col">Took</th>
            <th scope="col">What it found</th>
            <th scope="col">Trace</th>
          </tr>
        </thead>
        <tbody>
          {checks.slice(0, 25).map((c) => (
            <tr key={c.at}>
              <td>{dateTime.format(new Date(c.at))}</td>
              <td>
                <span className="monitor-state">
                  <span className={`svc-mark ${outcomeMarks[c.outcome]}`} aria-hidden="true" />
                  {outcomeLabels[c.outcome]}
                </span>
              </td>
              <td>{c.status ?? "–"}</td>
              <td>{c.outcome === "skipped" ? "–" : formatMs(c.durationMs)}</td>
              <td>{c.failureMessage ?? ""}</td>
              <td>
                {c.traceId ? (
                  <Link to="/projects/$projectId/traces/$traceId" params={{ projectId, traceId: c.traceId }}>
                    Open
                  </Link>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Settings({ monitor, etag, onSaved }: { monitor: Monitor; etag: string; onSaved: () => Promise<void> }) {
  const servicePages = useInfiniteQuery(servicesQuery(api, monitor.projectId));
  const services = useMemo(() => servicePages.data?.pages.flatMap((p) => p.data) ?? [], [servicePages.data]);
  const [draft, setDraft] = useState<MonitorDraft>(() => fromMonitor(monitor));
  const save = useMutation({
    mutationFn: (d: MonitorDraft) =>
      unwrapTagged(api.PATCH("/api/v1/monitors/{monitorId}", { params: { path: { monitorId: monitor.id }, header: { "If-Match": etag } }, body: toUpdate(d) })),
    onSuccess: onSaved,
  });
  return (
    <section aria-labelledby="settings" className="panel">
      <h2 id="settings" className="section-title">
        Settings
      </h2>
      <MonitorForm
        draft={draft}
        onChange={setDraft}
        onSubmit={() => save.mutate(draft)}
        environments={[]}
        services={services}
        pending={save.isPending}
        error={save.error}
        submitLabel="Save settings"
        label="Monitor settings"
        lockEnvironment
      />
    </section>
  );
}
