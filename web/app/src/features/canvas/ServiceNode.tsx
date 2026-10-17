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
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`;
}

export function formatRate(rate: number) {
  return rate === 0 ? "0%" : rate < 0.001 ? "<0.1%" : `${(rate * 100).toFixed(1)}%`;
}

/**
 * One service on the canvas. Health is shown by a shape and a word, never by
 * color alone. Memoized: a live update re-renders only the nodes whose data
 * changed.
 */
export const ServiceNode = memo(function ServiceNode({ id, data: base, selected }: NodeProps<ServiceNodeType>) {
  // Live values come from the canvas's metrics when it has them, so they
  // change without React Flow's graph changing.
  const live = useLiveNode(id);
  const data = live ? { ...base, ...live } : base;
  // Re-renders only when the zoom crosses the threshold.
  const compact = useStore(zoomedOut);
  if (compact) {
    return (
      <div className={`svc-node compact svc-${data.kind}${selected ? " selected" : ""}`} data-health={data.health}>
        <Handle type="target" position={Position.Left} isConnectable={false} />
        <span className={`svc-mark ${data.health}`} role="img" aria-label={healthLabels[data.health]} />
        <span className="svc-name">{data.name}</span>
        <Handle type="source" position={Position.Right} isConnectable={false} />
      </div>
    );
  }
  const metrics =
    data.p95Ms === undefined
      ? data.kind === "mock"
        ? "Answered by a mock"
        : "No traffic yet"
      : `p95 ${formatMs(data.p95Ms)} · ${formatRate(data.errorRate ?? 0)} err · ${data.rps ?? 0}/s`;
  return (
    <div className={`svc-node svc-${data.kind}${selected ? " selected" : ""}`} data-health={data.health}>
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <div className="svc-head">
        <span className="svc-name">{data.name}</span>
        <span className="svc-health">
          <span className={`svc-mark ${data.health}`} aria-hidden="true" />
          {healthLabels[data.health]}
        </span>
      </div>
      <div className="svc-sub">
        {kindLabels[data.kind]}
        {data.specVersion ? ` · ${data.specVersion}` : ""}
      </div>
      <div className="svc-metrics">{metrics}</div>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
});

export const nodeTypes = { service: ServiceNode };
