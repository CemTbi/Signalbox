// SPDX-License-Identifier: Apache-2.0

import type { Service } from "@signalbox/api-client";
import type { NodeChange } from "@xyflow/react";
import type { ServiceNode } from "../canvas/model";

/**
 * The canvas's nodes for a project's services, at their saved positions.
 * Nodes already on the canvas keep where they are and whether they're
 * selected, so a refetch doesn't undo a move that hasn't saved yet.
 */
export function servicesToNodes(services: Service[], current: ServiceNode[], draggable: boolean): ServiceNode[] {
  const byId = new Map(current.map((n) => [n.id, n]));
  return services.map((s) => {
    const prev = byId.get(s.id);
    return {
      id: s.id,
      type: "service",
      position: prev?.position ?? { x: s.position.x, y: s.position.y },
      selected: prev?.selected,
      draggable,
      data: { name: s.name, kind: "service", health: "unknown" },
    };
  });
}

/**
 * The nodes whose moves have finished: dropped after a drag, or moved with
 * the arrow keys. Moves still in progress aren't saved.
 */
export function finishedMoves(changes: NodeChange<ServiceNode>[]): string[] {
  const ids: string[] = [];
  for (const c of changes) {
    // A drag's last change says dragging: false and may carry no position;
    // the node's own position is what's saved.
    if (c.type === "position" && c.dragging !== true) ids.push(c.id);
  }
  return ids;
}
