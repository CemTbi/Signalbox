// SPDX-License-Identifier: Apache-2.0

import { BaseEdge, type EdgeProps, getBezierPath } from "@xyflow/react";
import { memo } from "react";
import { useLiveEdge } from "./LiveContext";
import type { CallEdge as CallEdgeType } from "./model";

/**
 * A line from caller to callee. Its label is the calls per second seen in
 * traces, read from the live metrics; a dashed line is a dependency the
 * team declared that traces haven't shown yet.
 */
export const CallEdge = memo(function CallEdge(props: EdgeProps<CallEdgeType>) {
  const { id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, data, markerEnd } = props;
  // A live entry wins even when it says there's no traffic now.
  const live = useLiveEdge(id);
  const rps = live ? live.rps : data?.rps;
  const [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
  const label = data?.declared ? "declared" : rps !== undefined ? `${rps}/s` : undefined;
  // The label is plain SVG text on a pill sized from its length. React
  // Flow's own labels measure their text with getBBox on every change, which
  // forces a layout per line and was the main cost of a live update.
  const width = label ? label.length * 6.6 + 10 : 0;
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} className={data?.declared ? "declared" : undefined} />
      {label ? (
        <g className="call-label" transform={`translate(${labelX} ${labelY})`}>
          <rect x={-width / 2} y={-9} width={width} height={18} rx={3} />
          <text textAnchor="middle" dominantBaseline="central">
            {label}
          </text>
        </g>
      ) : null}
    </>
  );
});

export const edgeTypes = { call: CallEdge };
