// SPDX-License-Identifier: Apache-2.0

import type { Span } from "@signalbox/api-client";
import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { type CSSProperties, useMemo, useState } from "react";
import { api, describeError } from "../../api";
import { type Crumb, LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, projectQuery, traceQuery, workspaceQuery } from "../../queries";
import { criticalPath, formatDuration, type Row, serviceColors, startMs, waterfall } from "./model";

const route = getRouteApi("/_app/projects/$projectId/traces/$traceId");

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" });

/** One trace as a waterfall: every span on a shared timeline, with its details. */
export function TracePage() {
  const { projectId, traceId } = route.useParams();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const environments = useQuery(environmentsQuery(api, projectId));
  const trace = useQuery(traceQuery(api, projectId, traceId));
  const spans = trace.data?.spans;
  const rows = useMemo(() => waterfall(spans ?? []), [spans]);
  const critical = useMemo(() => criticalPath(spans ?? []), [spans]);
  const colors = useMemo(() => serviceColors(spans ?? []), [spans]);
  const [selected, setSelected] = useState<string | null>(null);

  const crumbs: Crumb[] = [
    { label: "Workspaces", to: "/workspaces" },
    ...(project.data && workspace.data
      ? [
          { label: workspace.data.name, to: "/workspaces/$workspaceId", params: { workspaceId: project.data.workspaceId } },
          { label: project.data.name, to: "/projects/$projectId", params: { projectId } },
        ]
      : []),
    { label: "Traces", to: "/projects/$projectId/traces", params: { projectId } },
    { label: traceId.slice(0, 8) },
  ];
  if (trace.isPending) return <Loading label="Loading the trace…" />;
  if (trace.isError) {
    return (
      <div className="stack">
        <PageHeader title="Trace" documentTitle="Trace" crumbs={crumbs} />
        <LoadError message={describeError(trace.error)} onRetry={() => void trace.refetch()} />
        <p className="field-hint">
          Spans arrive when the services that handled the request send them with an ingest key. A request to an API that isn't traced has no trace.
        </p>
      </div>
    );
  }

  const t = trace.data;
  const root = rows[0]?.span;
  const total = Math.max(...rows.map((r) => r.endMs), 0.001);
  const errors = t.spans.filter((s) => s.status === "error").length;
  const environment = environments.data?.data.find((e) => e.id === t.environmentId)?.name;
  const shown = t.spans.find((s) => s.spanId === selected) ?? root;
  return (
    <div className="stack">
      <PageHeader
        title={root ? `${root.service}: ${root.name}` : "Trace"}
        documentTitle={`Trace ${traceId.slice(0, 8)}`}
        crumbs={crumbs}
        meta={
          <p className="lede">
            {formatDuration(total)} · {t.spans.length === 1 ? "1 span" : `${t.spans.length} spans`}
            {errors ? ` · ${errors === 1 ? "1 error" : `${errors} errors`}` : ""} · {colors.size === 1 ? "1 service" : `${colors.size} services`}
            {root ? ` · ${timeFormat.format(new Date(startMs(root)))}` : ""}
            {environment ? ` · ${environment}` : ""}
            <span className="mono muted"> · {traceId}</span>
          </p>
        }
      />
      {arriving(t.spans) ? (
        <p className="notice" role="status">
          This trace is a few seconds old, and each service sends its spans every few seconds, so more may arrive. The page checks again by itself.
        </p>
      ) : null}
      <ul className="legend" aria-label="Services">
        {[...colors].map(([service, color]) => (
          <li key={service}>
            <span className="swatch" style={{ background: color }} aria-hidden="true" />
            {service}
          </li>
        ))}
        <li>
          <span className="swatch critical" aria-hidden="true" />
          Critical path
        </li>
      </ul>
      <Waterfall rows={rows} total={total} colors={colors} critical={critical} selected={shown?.spanId} onSelect={setSelected} />
      {shown ? <SpanDetails span={shown} offsetMs={rows.find((r) => r.span.spanId === shown.spanId)?.offsetMs ?? 0} /> : null}
      <p>
        <Link to="/projects/$projectId/traces" params={{ projectId }}>
          Search traces
        </Link>
      </p>
    </div>
  );
}

/** Whether a trace ended so recently that its services may still be sending spans. */
function arriving(spans: Span[]): boolean {
  const end = Math.max(...spans.map((s) => startMs(s) + s.durationMs));
  return Date.now() - end < recentMs;
}

/** How long after its end a trace may still gain spans. */
const recentMs = 30_000;

function Waterfall({
  rows,
  total,
  colors,
  critical,
  selected,
  onSelect,
}: {
  rows: Row[];
  total: number;
  colors: Map<string, string>;
  critical: Set<string>;
  selected?: string;
  onSelect: (spanId: string) => void;
}) {
  const ticks = [0, 0.25, 0.5, 0.75, 1];
  return (
    <table className="waterfall">
      <caption className="visually-hidden">Spans of the trace, in tree order, with when each started and how long it took.</caption>
      <thead>
        <tr>
          <th scope="col">Span</th>
          <th scope="col" className="timeline-head">
            <span className="visually-hidden">Timeline</span>
            {ticks.map((f) => (
              <span key={f} className="tick" style={{ left: `${f * 100}%` }} aria-hidden="true">
                {formatDuration(f * total)}
              </span>
            ))}
          </th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const s = r.span;
          const isSelected = s.spanId === selected;
          const bar: CSSProperties = {
            left: `${(r.offsetMs / total) * 100}%`,
            width: `max(2px, ${(s.durationMs / total) * 100}%)`,
            background: colors.get(s.service),
          };
          return (
            <tr key={s.spanId} className={isSelected ? "selected" : undefined}>
              <th scope="row" style={{ paddingLeft: `${8 + r.depth * 14}px` }}>
                <button type="button" className="span-name" aria-pressed={isSelected} onClick={() => onSelect(s.spanId)}>
                  <span className="swatch" style={{ background: colors.get(s.service) }} aria-hidden="true" />
                  <span className="mono">{s.service}</span> {s.name}
                  {s.status === "error" ? <span className="badge bad">Error</span> : null}
                  {r.orphan ? (
                    <span className="badge" title="The span it's part of wasn't sent here, such as a request Signalbox or another untraced caller sent.">
                      Parent not received
                    </span>
                  ) : null}
                  <span className="span-duration">{formatDuration(s.durationMs)}</span>
                </button>
              </th>
              <td className="timeline">
                <span className={critical.has(s.spanId) ? "bar critical" : "bar"} style={bar} />
                <span className="visually-hidden">
                  Starts at {formatDuration(r.offsetMs)}
                  {critical.has(s.spanId) ? ", on the critical path" : ""}
                </span>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function SpanDetails({ span: s, offsetMs }: { span: Span; offsetMs: number }) {
  const attributes = Object.entries(s.attributes).sort(([a], [b]) => a.localeCompare(b));
  const resource = Object.entries(s.resource).sort(([a], [b]) => a.localeCompare(b));
  const t0 = startMs(s) - offsetMs;
  return (
    <section className="panel stack-tight" aria-labelledby="span-details">
      <h2 id="span-details" className="section-title">
        {s.service}: {s.name}
      </h2>
      <dl className="facts">
        <div>
          <dt>Duration</dt>
          <dd>{formatDuration(s.durationMs)}</dd>
        </div>
        <div>
          <dt>Starts</dt>
          <dd>{formatDuration(offsetMs)} into the trace</dd>
        </div>
        <div>
          <dt>Kind</dt>
          <dd>{s.kind}</dd>
        </div>
        <div>
          <dt>Status</dt>
          <dd>
            {s.status}
            {s.statusMessage ? `: ${s.statusMessage}` : ""}
          </dd>
        </div>
        <div>
          <dt>Operation</dt>
          <dd className="mono">{s.operation}</dd>
        </div>
        <div>
          <dt>Span ID</dt>
          <dd className="mono">{s.spanId}</dd>
        </div>
        {s.scope ? (
          <div>
            <dt>Instrumentation</dt>
            <dd className="mono">
              {s.scope.name}
              {s.scope.version ? ` ${s.scope.version}` : ""}
            </dd>
          </div>
        ) : null}
      </dl>
      <Attributes title="Attributes" entries={attributes} />
      {s.events.length > 0 ? (
        <>
          <h3 className="subsection-title">Events</h3>
          <ul className="rows">
            {s.events.map((e, i) => (
              <li key={i} className="row">
                <span className="row-main">
                  <span className="row-title">{e.name}</span>
                  <span className="row-sub">
                    at {formatDuration(Date.parse(e.time) - t0)}
                    {Object.entries(e.attributes).map(([k, v]) => ` · ${k}: ${v}`)}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {s.links.length > 0 ? (
        <>
          <h3 className="subsection-title">Links</h3>
          <ul>
            {s.links.map((l) => (
              <li key={l.traceId + l.spanId} className="mono">
                {l.traceId} / {l.spanId}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <Attributes title="Resource" entries={resource} />
    </section>
  );
}

function Attributes({ title, entries }: { title: string; entries: [string, string][] }) {
  if (entries.length === 0) return null;
  return (
    <>
      <h3 className="subsection-title">{title}</h3>
      <table className="kv">
        <tbody>
          {entries.map(([k, v]) => (
            <tr key={k}>
              <th scope="row" className="mono">
                {k}
              </th>
              <td className="mono">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
