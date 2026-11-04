// SPDX-License-Identifier: Apache-2.0

import { Handle, type NodeProps, Position, type ReactFlowState, useStore } from "@xyflow/react";
import { memo } from "react";
import { useLiveNode } from "./LiveContext";
import type { Health, ServiceKind, ServiceNode as ServiceNodeType } from "./model";

const kindLabels: Record<ServiceKind, string> = {
  service: "Service",
  database: "Database",
  external: "External host",
  mock: "Mock only",
};

/** Below this zoom, nodes show only their name and health. */
export const COMPACT_ZOOM = 0.55;
const zoomedOut = (s: ReactFlowState) => s.transform[2] < COMPACT_ZOOM;

const healthLabels: Record<Health, string> = {
  ok: "Healthy",
  warn: "Degraded",
  down: "Down",
  unknown: "No checks",
};

export function formatMs(ms: number) {
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms < 1) return "<1 ms";
  return `${ms < 10 ? Number(ms.toFixed(1)) : Math.round(ms)} ms`;
}

/** A rate, short enough for a node or a line's label. */
export function formatPerSecond(rate: number) {
  if (rate === 0) return "0/s";
  if (rate < 0.01) return "<0.01/s";
  return `${rate < 10 ? Number(rate.toFixed(2)) : Math.round(rate)}/s`;
}

export function formatRate(rate: number) {
  return rate === 0 ? "0%" : rate < 0.001 ? "<0.1%" : `${(rate * 100).toFixed(1)}%`;
}

/**
 * One service on the canvas. Health is shown by a shape and a word, never by
 * color alone. Memoized: a live update re-renders only the nodes whose data
 * changed.
 */
export const ServiceNode = memo(function ServiceNode({ id, data: base, selected, isConnectable }: NodeProps<ServiceNodeType>) {
  // Live values come from the canvas's metrics when it has them, so they
  // change without React Flow's graph changing.
  const live = useLiveNode(id);
  const data = live ? { ...base, ...live } : base;
  // Re-renders only when the zoom crosses the threshold.
  const compact = useStore(zoomedOut);
  if (compact) {
    return (
      <div className={`svc-node compact svc-${data.kind}${selected ? " selected" : ""}`} data-health={data.health} title={data.healthDetail}>
        <Handle type="target" position={Position.Left} isConnectable={isConnectable} />
        <span className={`svc-mark ${data.health}`} role="img" aria-label={healthLabels[data.health]} />
        <span className="svc-name">{data.name}</span>
        <Handle type="source" position={Position.Right} isConnectable={isConnectable} />
      </div>
    );
  }
  // Databases and hosts have the rate of the calls to them, not a p95 of
  // their own, and no checks yet.
  const monitored = data.kind !== "database" && data.kind !== "external";
  const quiet = data.p95Ms === undefined && !data.rps;
  const metrics = quiet
    ? data.kind === "mock"
      ? "Answered by a mock"
      : "No recent traffic"
    : `${data.p95Ms === undefined ? "" : `p95 ${formatMs(data.p95Ms)} · `}${formatRate(data.errorRate ?? 0)} err · ${formatPerSecond(data.rps ?? 0)}`;
  return (
    <div className={`svc-node svc-${data.kind}${selected ? " selected" : ""}`} data-health={data.health} title={data.healthDetail}>
      <Handle type="target" position={Position.Left} isConnectable={isConnectable} />
      <div className="svc-head">
        <span className="svc-name">{data.name}</span>
        {monitored ? (
          <span className="svc-health">
            <span className={`svc-mark ${data.health}`} aria-hidden="true" />
            {healthLabels[data.health]}
          </span>
        ) : null}
      </div>
      <div className="svc-sub">
        {kindLabels[data.kind]}
        {data.specVersion ? ` · ${data.specVersion}` : ""}
        {data.system ? ` · ${data.system}` : ""}
      </div>
      <div className="svc-metrics">{metrics}</div>
      <Handle type="source" position={Position.Right} isConnectable={isConnectable} />
    </div>
  );
});

export const nodeTypes = { service: ServiceNode };
