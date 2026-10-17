// SPDX-License-Identifier: Apache-2.0

import type { CallEdge, ServiceNode } from "./model";
import { NODE_HEIGHT, NODE_WIDTH } from "./model";

/** Positions keyed by node ID. */
export type Positions = Map<string, { x: number; y: number }>;

/**
 * Lays the graph out left to right with ELK's layered algorithm, in a web
 * worker so the page stays responsive. ELK and its worker load on first use.
 */
export async function layoutWithElk(nodes: ServiceNode[], edges: CallEdge[]): Promise<Positions> {
  const elk = await elkInstance();
  const graph = await elk.layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.layered.spacing.nodeNodeBetweenLayers": "96",
      "elk.spacing.nodeNode": "28",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.edgeRouting": "SPLINES",
    },
    children: nodes.map((n) => ({ id: n.id, width: NODE_WIDTH, height: NODE_HEIGHT })),
    edges: edges.map((e) => ({ id: e.id, sources: [e.source], targets: [e.target] })),
  });
  const out: Positions = new Map();
  for (const c of graph.children ?? []) out.set(c.id, { x: c.x ?? 0, y: c.y ?? 0 });
  return out;
}

let elkPromise: Promise<import("elkjs/lib/elk-api").ELK> | undefined;
function elkInstance() {
  elkPromise ??= Promise.all([import("elkjs/lib/elk-api"), import("elkjs/lib/elk-worker.min.js?worker")]).then(
    ([api, worker]) => new api.default({ workerFactory: () => new worker.default() }),
  );
  return elkPromise;
}
