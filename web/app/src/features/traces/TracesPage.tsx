// SPDX-License-Identifier: Apache-2.0

import type { TraceSummary } from "@signalbox/api-client";
import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useId, useMemo, useState } from "react";
import { api, describeError } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, projectQuery, type TraceSearch, tracedServicesQuery, tracesQuery, workspaceQuery } from "../../queries";
import { formatDuration, ranges } from "./model";

const route = getRouteApi("/_app/projects/$projectId/traces");

/** What the page's address says to search for. */
export interface TracesSearch {
  environment?: string;
  range?: string;
  service?: string;
  operation?: string;
  errors?: boolean;
  minMs?: number;
  attribute?: string;
}

export function validateTracesSearch(s: Record<string, unknown>): TracesSearch {
  const text = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);
  const out: TracesSearch = {
    environment: text(s.environment),
    range: ranges.some((r) => r.value === s.range) ? (s.range as string) : undefined,
    service: text(s.service),
    operation: text(s.operation),
    attribute: text(s.attribute),
    errors: s.errors === true || s.errors === "true" ? true : undefined,
  };
  const min = Number(s.minMs);
  if (Number.isFinite(min) && min > 0) out.minMs = min;
  return Object.fromEntries(Object.entries(out).filter(([, v]) => v !== undefined)) as TracesSearch;
}

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "medium" });

/** A project's traces: search them by service, operation, errors, duration and attribute. */
export function TracesPage() {
  const { projectId } = route.useParams();
  const search = route.useSearch();
  const navigate = useNavigate({ from: "/projects/$projectId/traces" });
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const environments = useQuery(environmentsQuery(api, projectId));
  // Production is where traces usually come from, so it's the default.
  const envList = environments.data?.data ?? [];
  const environmentId = search.environment ?? envList.find((e) => e.kind === "production")?.id ?? envList[0]?.id ?? "";
  const range = ranges.find((r) => r.value === search.range) ?? ranges[1];
  // The window is fixed when the search changes or Refresh is pressed, so
  // the query doesn't change on every render.
  const [refreshed, setRefreshed] = useState(() => Date.now());
  const period = useMemo(() => ({ to: new Date(refreshed).toISOString(), from: new Date(refreshed - range.ms).toISOString() }), [refreshed, range.ms]);
  const query: TraceSearch = {
    ...period,
    service: search.service,
    operation: search.operation,
    errors: search.errors,
    minDurationMs: search.minMs,
    attribute: search.attribute,
  };
  const traces = useQuery({ ...tracesQuery(api, environmentId, query), enabled: environmentId !== "" });
  const services = useQuery({ ...tracedServicesQuery(api, environmentId, period.from, period.to), enabled: environmentId !== "" });

  const update = (patch: Partial<TracesSearch>) => {
    setRefreshed(Date.now());
    void navigate({ search: (prev) => validateTracesSearch({ ...prev, ...patch }), replace: true });
  };

  if (project.isPending || environments.isPending) return <Loading label="Loading traces…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  if (environments.isError) return <LoadError message={describeError(environments.error)} onRetry={() => void environments.refetch()} />;
  const p = project.data;

  return (
    <div className="stack">
      <PageHeader
        title="Traces"
        documentTitle={`Traces · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId: p.id } },
          { label: "Traces" },
        ]}
        actions={
          <button type="button" className="button secondary" onClick={() => setRefreshed(Date.now())}>
            Refresh
          </button>
        }
      />
      <Filters
        key={JSON.stringify(search)}
        search={search}
        environments={envList.map((e) => ({ id: e.id, name: e.name }))}
        environmentId={environmentId}
        services={(services.data?.data ?? []).map((s) => s.service)}
        onChange={update}
      />
      <section aria-labelledby="results" className="stack-tight">
        <h2 id="results" className="section-title">
          {range.label}
        </h2>
        {traces.isPending ? <Loading label="Searching traces…" /> : null}
        {traces.isError ? <LoadError message={describeError(traces.error)} onRetry={() => void traces.refetch()} /> : null}
        {traces.data && traces.data.data.length === 0 ? (
          <div className="empty-state">
            <p className="empty-title">No traces found</p>
            <p>
              Try a longer time range or fewer filters. Services send traces with an ingest key from{" "}
              <Link to="/projects/$projectId/ingest-keys" params={{ projectId }}>
                Ingest keys
              </Link>
              .
            </p>
          </div>
        ) : null}
        {traces.data && traces.data.data.length > 0 ? <TraceList projectId={projectId} traces={traces.data.data} /> : null}
      </section>
    </div>
  );
}

function Filters({
  search,
  environments,
  environmentId,
  services,
  onChange,
}: {
  search: TracesSearch;
  environments: { id: string; name: string }[];
  environmentId: string;
  services: string[];
  onChange: (patch: Partial<TracesSearch>) => void;
}) {
  const ids = { env: useId(), range: useId(), service: useId(), operation: useId(), min: useId(), attribute: useId(), errors: useId() };
  const [operation, setOperation] = useState(search.operation ?? "");
  const [minMs, setMinMs] = useState(search.minMs ? String(search.minMs) : "");
  const [attribute, setAttribute] = useState(search.attribute ?? "");
  const choices = search.service && !services.includes(search.service) ? [search.service, ...services] : services;
  function submit(e: FormEvent) {
    e.preventDefault();
    onChange({ operation: operation.trim() || undefined, minMs: Number(minMs) > 0 ? Number(minMs) : undefined, attribute: attribute.trim() || undefined });
  }
  return (
    <form className="trace-filters" onSubmit={submit} aria-label="Search traces">
      <div className="field">
        <label htmlFor={ids.env}>Environment</label>
        <select id={ids.env} value={environmentId} onChange={(e) => onChange({ environment: e.target.value })}>
          {environments.map((e) => (
            <option key={e.id} value={e.id}>
              {e.name}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor={ids.range}>Time</label>
        <select id={ids.range} value={search.range ?? "1h"} onChange={(e) => onChange({ range: e.target.value })}>
          {ranges.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor={ids.service}>Service</label>
        <select id={ids.service} value={search.service ?? ""} onChange={(e) => onChange({ service: e.target.value || undefined })}>
          <option value="">Any service</option>
          {choices.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor={ids.operation}>Operation</label>
        <input id={ids.operation} value={operation} onChange={(e) => setOperation(e.target.value)} placeholder="POST /orders" />
      </div>
      <div className="field">
        <label htmlFor={ids.min}>At least (ms)</label>
        <input id={ids.min} inputMode="decimal" value={minMs} onChange={(e) => setMinMs(e.target.value)} placeholder="250" />
      </div>
      <div className="field">
        <label htmlFor={ids.attribute}>Attribute</label>
        <input id={ids.attribute} value={attribute} onChange={(e) => setAttribute(e.target.value)} placeholder="http.route=/orders" />
      </div>
      <div className="checkbox-field">
        <input id={ids.errors} type="checkbox" checked={search.errors ?? false} onChange={(e) => onChange({ errors: e.target.checked || undefined })} />
        <label htmlFor={ids.errors}>Only with errors</label>
      </div>
      <button type="submit" className="button primary">
        Search
      </button>
    </form>
  );
}

function TraceList({ projectId, traces }: { projectId: string; traces: TraceSummary[] }) {
  const longest = Math.max(...traces.map((t) => t.durationMs), 1);
  return (
    <table className="traces">
      <thead>
        <tr>
          <th scope="col">Trace</th>
          <th scope="col">Started</th>
          <th scope="col">Duration</th>
          <th scope="col">Spans</th>
          <th scope="col">Services</th>
        </tr>
      </thead>
      <tbody>
        {traces.map((t) => (
          <tr key={t.traceId}>
            <td>
              <Link to="/projects/$projectId/traces/$traceId" params={{ projectId, traceId: t.traceId }} className="trace-name">
                <span className="mono">{t.rootService}</span>: {t.rootName}
              </Link>
              {t.rootMissing ? (
                <span className="badge" title="The span that started the trace wasn't sent here, such as a request Signalbox sent.">
                  Root not received
                </span>
              ) : null}
              {t.errors > 0 ? <span className="badge bad">{t.errors === 1 ? "1 error" : `${t.errors} errors`}</span> : null}
            </td>
            <td className="muted">{timeFormat.format(new Date(t.start))}</td>
            <td>
              <span className="duration-bar" aria-hidden="true" style={{ width: `${Math.max(2, (t.durationMs / longest) * 100)}%` }} />
              {formatDuration(t.durationMs)}
            </td>
            <td>{t.spans}</td>
            <td className="muted">{t.services.join(", ")}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
