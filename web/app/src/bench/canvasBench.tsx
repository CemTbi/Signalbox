// SPDX-License-Identifier: Apache-2.0

// The canvas benchmark behind spike 3 and ADR-0018. Development only: the
// production build doesn't include it. Query parameters:
//
//   nodes=200         services on the canvas
//   layout=elk|dagre  layout engine
//   every=2000        milliseconds between live metric updates; 0 for none
//   update=store|patch|all
//                     store: live metrics outside React Flow (LiveMetrics);
//                     patch: updateNodeData per node; all: replace every node
//
// window.__bench exposes timings for e2e/bench/canvas.mjs.

import { ReactFlow, ReactFlowProvider, useReactFlow } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { StrictMode, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "../styles.css";
import { demoShop, nextMetrics, rng } from "../features/canvas/demo";
import { edgeTypes } from "../features/canvas/CallEdge";
import { LiveMetricsContext } from "../features/canvas/LiveContext";
import { type LiveNode, LiveMetrics } from "../features/canvas/live";
import { layoutWithElk, type Positions } from "../features/canvas/layout";
import { type CallEdge, NODE_HEIGHT, NODE_WIDTH, type ServiceNode } from "../features/canvas/model";
import { nodeTypes } from "../features/canvas/ServiceNode";

const params = new URLSearchParams(location.search);
const count = Number(params.get("nodes") ?? 200);
const layout = params.get("layout") === "dagre" ? "dagre" : "elk";
const every = Number(params.get("every") ?? 2000);
const mode = (["store", "patch", "all"] as const).find((m) => m === params.get("update")) ?? "store";

type Bench = {
  ready: boolean;
  nodes: number;
  edges: number;
  layoutMs?: number;
  renderMs?: number;
  updates: number[];
  frames(ms: number, pan?: boolean): Promise<number[]>;
};
const bench: Bench = { ready: false, nodes: 0, edges: 0, updates: [], frames: async () => [] };
(window as unknown as { __bench: Bench }).__bench = bench;

const live = new LiveMetrics();
// hold=0 keeps showing updates while the canvas moves.
const holdWhileMoving = params.get("hold") !== "0";

function Canvas() {
  const flow = useReactFlow<ServiceNode, CallEdge>();
  const [graph, setGraph] = useState<{ nodes: ServiceNode[]; edges: CallEdge[] }>();
  const start = useRef(performance.now());
  const pendingUpdate = useRef<number | undefined>(undefined);

  useEffect(() => {
    const { nodes, edges } = demoShop(count);
    const t0 = performance.now();
    (layout === "elk" ? layoutWithElk : layoutWithDagre)(nodes, edges).then((pos) => {
      bench.layoutMs = performance.now() - t0;
      bench.nodes = nodes.length;
      bench.edges = edges.length;
      setGraph({ nodes: nodes.map((n) => ({ ...n, position: pos.get(n.id) ?? n.position })), edges });
    });
  }, []);

  // First render: the frame after React commits the nodes.
  useLayoutEffect(() => {
    if (!graph) return;
    requestAnimationFrame(() => {
      bench.renderMs = performance.now() - start.current;
      bench.ready = true;
    });
  }, [graph]);

  // Live updates.
  useEffect(() => {
    if (!graph || every <= 0) return;
    const random = rng(7);
    let current = graph.nodes;
    const id = setInterval(() => {
      const nodes = mode === "store" ? current : flow.getNodes();
      const edges = flow.getEdges();
      const { nodeData, edgeData } = nextMetrics(nodes, edges, random);
      const t0 = performance.now();
      pendingUpdate.current = t0;
      if (mode === "store") {
        // React Flow's graph stays as it is.
        current = current.map((n) => ({ ...n, data: { ...n.data, ...nodeData.get(n.id) } }));
        live.update(nodeData as Map<string, LiveNode>, edgeData);
      } else {
        if (mode === "all") {
          flow.setNodes(nodes.map((n) => ({ ...n, data: { ...n.data, ...nodeData.get(n.id) } })));
        } else {
          for (const [nid, d] of nodeData) flow.updateNodeData(nid, d);
        }
        flow.setEdges(edges.map((e) => ({ ...e, data: { ...e.data, rps: edgeData.get(e.id) } })));
      }
      requestAnimationFrame(() => {
        requestAnimationFrame(() => bench.updates.push(performance.now() - t0));
      });
    }, every);
    return () => clearInterval(id);
  }, [graph, flow]);

  bench.frames = (ms, pan) =>
    new Promise((resolve) => {
      const deltas: number[] = [];
      let last = performance.now();
      const end = last + ms;
      const vp = flow.getViewport();
      // A scripted pan may not fire the gesture events, so hold here too.
      if (pan && holdWhileMoving) live.hold(true);
      const step = (now: number) => {
        deltas.push(now - last);
        last = now;
        if (pan) {
          const t = (now - (end - ms)) / ms;
          flow.setViewport({ x: vp.x - 1500 * Math.sin(t * Math.PI), y: vp.y - 600 * Math.sin(t * 2 * Math.PI), zoom: vp.zoom });
        }
        if (now < end) requestAnimationFrame(step);
        else {
          if (pan) {
            flow.setViewport(vp);
            if (holdWhileMoving) live.hold(false);
          }
          resolve(deltas);
        }
      };
      requestAnimationFrame(step);
    });

  // labels=0 draws plain lines, to measure what rate labels cost.
  const edges = useMemo(() => graph?.edges.map((e) => ({ ...e, type: params.get("labels") === "0" ? "default" : "call" })), [graph]);
  if (!graph || !edges) return <p style={{ padding: 16 }}>Laying out {count} services…</p>;
  return (
    <LiveMetricsContext.Provider value={mode === "store" ? live : undefined}>
    <ReactFlow
      defaultNodes={graph.nodes}
      defaultEdges={edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onMoveStart={() => holdWhileMoving && live.hold(true)}
      onMoveEnd={() => holdWhileMoving && live.hold(false)}
      fitView
      minZoom={0.1}
      proOptions={{ hideAttribution: true }}
      aria-label="Service canvas"
      colorMode="system"
      onlyRenderVisibleElements={params.get("visible") === "1"}
    />
    </LiveMetricsContext.Provider>
  );
}

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    <div style={{ width: "100vw", height: "100vh" }}>
      <ReactFlowProvider>
        <Canvas />
      </ReactFlowProvider>
    </div>
  </StrictMode>,
);

// dagre, which ADR-0018 compares with ELK. Only the benchmark uses it.
async function layoutWithDagre(nodes: ServiceNode[], edges: CallEdge[]): Promise<Positions> {
  const { default: dagre } = await import("@dagrejs/dagre");
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", ranksep: 96, nodesep: 28 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  const out: Positions = new Map();
  for (const id of g.nodes()) {
    const p = g.node(id);
    // dagre gives centers; React Flow wants top-left corners.
    out.set(id, { x: p.x - NODE_WIDTH / 2, y: p.y - NODE_HEIGHT / 2 });
  }
  return out;
}
