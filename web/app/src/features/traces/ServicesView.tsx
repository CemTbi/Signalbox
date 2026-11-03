// SPDX-License-Identifier: Apache-2.0

import type { ServiceEdge, ServiceStats } from "@signalbox/api-client";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { api, describeError } from "../../api";
import { LoadError, Loading } from "../../components/Page";
import { graphQuery, operationsQuery } from "../../queries";
import { formatDuration } from "./model";

const number = new Intl.NumberFormat();

/** The share of calls that failed, as a person reads it. */
export function errorRate(calls: number, errors: number): string {
  if (calls === 0 || errors === 0) return "0%";
  const pct = (errors / calls) * 100;
  return pct < 0.1 ? "<0.1%" : `${pct < 10 ? pct.toFixed(1) : Math.round(pct)}%`;
}

const kindLabel: Record<ServiceEdge["toKind"], string> = { service: "", database: "Database", external: "External" };

/**
 * An environment's services over a time range: the requests each handled,
 * its operations, and who called whom.
 */
export function ServicesView({
  projectId,
  environmentId,
  services,
  from,
  to,
}: {
  projectId: string;
  environmentId: string;
  services: { data?: { data: ServiceStats[] }; isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown };
  from: string;
  to: string;
}) {
  const graph = useQuery(graphQuery(api, environmentId, from, to));
  return (
    <div className="stack">
      <section aria-labelledby="services-title" className="stack-tight">
        <h2 id="services-title" className="section-title">
          Services
        </h2>
        {services.isPending ? <Loading label="Loading services…" /> : null}
        {services.isError ? <LoadError message={describeError(services.error)} onRetry={() => void services.refetch()} /> : null}
        {services.data && services.data.data.length === 0 ? (
          <p className="field-hint">No service sent spans in this time range.</p>
        ) : null}
        {services.data && services.data.data.length > 0 ? (
          <table className="traces">
            <thead>
              <tr>
                <th scope="col">Service</th>
                <th scope="col">Requests</th>
                <th scope="col">Errors</th>
                <th scope="col">p50</th>
                <th scope="col">p95</th>
                <th scope="col">p99</th>
              </tr>
            </thead>
            <tbody>
              {services.data.data.map((s) => (
                <ServiceRow key={s.service} projectId={projectId} environmentId={environmentId} stats={s} from={from} to={to} />
              ))}
            </tbody>
          </table>
        ) : null}
      </section>
      <section aria-labelledby="calls-title" className="stack-tight">
        <h2 id="calls-title" className="section-title">
          Calls
        </h2>
        <p className="field-hint">Who called whom, as the callers saw it. Lines appear about two minutes after the calls.</p>
        {graph.isPending ? <Loading label="Loading calls…" /> : null}
        {graph.isError ? <LoadError message={describeError(graph.error)} onRetry={() => void graph.refetch()} /> : null}
        {graph.data && graph.data.edges.length === 0 ? <p className="field-hint">No calls between services in this time range.</p> : null}
        {graph.data && graph.data.edges.length > 0 ? (
          <table className="traces">
            <thead>
              <tr>
                <th scope="col">From</th>
                <th scope="col">To</th>
                <th scope="col">Calls</th>
                <th scope="col">Errors</th>
                <th scope="col">p95</th>
              </tr>
            </thead>
            <tbody>
              {graph.data.edges.map((e) => (
                <tr key={`${e.from}>${e.toKind}:${e.to}:${e.system ?? ""}`}>
                  <td className="mono">{e.from}</td>
                  <td>
                    <span className="mono">{e.to}</span>{" "}
                    {e.toKind !== "service" ? (
                      <span className="badge">
                        {kindLabel[e.toKind]}
                        {e.system && e.system !== e.to ? `, ${e.system}` : ""}
                      </span>
                    ) : null}
                  </td>
                  <td>{number.format(e.calls)}</td>
                  <td>{errorRate(e.calls, e.errors)}</td>
                  <td>{formatDuration(e.p95Ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
      </section>
    </div>
  );
}

function ServiceRow({
  projectId,
  environmentId,
  stats: s,
  from,
  to,
}: {
  projectId: string;
  environmentId: string;
  stats: ServiceStats;
  from: string;
  to: string;
}) {
  const [open, setOpen] = useState(false);
  const detail = useId();
  return (
    <>
      <tr>
        <td>
          <button type="button" className="link-button mono" aria-expanded={open} aria-controls={detail} onClick={() => setOpen(!open)}>
            {s.service}
          </button>{" "}
          <Link to="/projects/$projectId/traces" params={{ projectId }} search={{ environment: environmentId, service: s.service }} className="muted">
            traces
          </Link>
        </td>
        <td>{number.format(s.calls)}</td>
        <td>{errorRate(s.calls, s.errors)}</td>
        <td>{formatDuration(s.p50Ms)}</td>
        <td>{formatDuration(s.p95Ms)}</td>
        <td>{formatDuration(s.p99Ms)}</td>
      </tr>
      {open ? (
        <tr id={detail}>
          <td colSpan={6}>
            <Operations environmentId={environmentId} service={s.service} from={from} to={to} />
          </td>
        </tr>
      ) : null}
    </>
  );
}

function Operations({ environmentId, service, from, to }: { environmentId: string; service: string; from: string; to: string }) {
  const ops = useQuery(operationsQuery(api, environmentId, service, from, to));
  if (ops.isPending) return <Loading label="Loading operations…" />;
  if (ops.isError) return <LoadError message={describeError(ops.error)} onRetry={() => void ops.refetch()} />;
  return (
    <table className="kv operations" aria-label={`Operations of ${service}`}>
      <thead>
        <tr>
          <th scope="col">Operation</th>
          <th scope="col">Kind</th>
          <th scope="col">Spans</th>
          <th scope="col">Errors</th>
          <th scope="col">p95</th>
        </tr>
      </thead>
      <tbody>
        {ops.data.data.map((o) => (
          <tr key={`${o.operation}|${o.kind}`}>
            <td className="mono">{o.operation}</td>
            <td>{o.kind}</td>
            <td>{number.format(o.calls)}</td>
            <td>{errorRate(o.calls, o.errors)}</td>
            <td>{formatDuration(o.p95Ms)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
