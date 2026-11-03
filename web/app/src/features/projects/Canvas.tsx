// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { applyNodeChanges, Background, BackgroundVariant, type Connection, Controls, type NodeChange, ReactFlow, useReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { edgeTypes } from "../canvas/CallEdge";
import { layoutWithElk } from "../canvas/layout";
import { LiveMetricsContext } from "../canvas/LiveContext";
import { LiveMetrics } from "../canvas/live";
import type { ServiceNode } from "../canvas/model";
import { nodeTypes } from "../canvas/ServiceNode";
import { canvasLayoutKey, canvasLayoutQuery, canvasLiveQuery, dependenciesQuery, servicesKey, servicesQuery } from "../../queries";
import { useDependencyChanges } from "./CanvasPanels";
import { canvasEdges, finishedMoves, liveValues, servicesToNodes, targetsOf, targetsToNodes } from "./placement";

const GRID: [number, number] = [8, 8];

/** How long the canvas waits after a move before saving, so a run of arrow-key moves saves once. */
const SAVE_DELAY_MS = 400;

/** How the canvas fits its nodes: never enlarged, and clear of the bar along its top. */
const FIT = { maxZoom: 1, padding: { top: "56px", right: "24px", bottom: "24px", left: "24px" } } as const;

type SaveState = { state: "idle" } | { state: "saving" } | { state: "saved" } | { state: "failed"; message: string };

/**
 * A project's canvas: each service is a node at the place someone put it,
 * showing its traffic in one environment, with lines for who calls whom.
 * Editors move nodes by dragging them, or by selecting one and using the
 * arrow keys, and the canvas saves where they land for everyone.
 */
export default function Canvas({ projectId, environmentId, canEdit }: { projectId: string; environmentId: string; canEdit: boolean }) {
  return (
    <ReactFlowProvider>
      <ProjectCanvas projectId={projectId} environmentId={environmentId} canEdit={canEdit} />
    </ReactFlowProvider>
  );
}

/** Keeps the first of equal values, so what depends on it runs only when the key changes. */
function useStable<T>(value: T, key: string): T {
  const ref = useRef({ key, value });
  if (ref.current.key !== key) ref.current = { key, value };
  return ref.current.value;
}

function ProjectCanvas({ projectId, environmentId, canEdit }: { projectId: string; environmentId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const flow = useReactFlow<ServiceNode>();
  const services = useInfiniteQuery(servicesQuery(api, projectId));
  const layout = useQuery(canvasLayoutQuery(api, projectId));
  const dependencies = useQuery(dependenciesQuery(api, projectId));
  const live = useQuery(canvasLiveQuery(api, projectId, environmentId));
  const { create: declare } = useDependencyChanges(projectId);
  // A new array only when the services change, so the effect below runs then.
  const list = useMemo(() => services.data?.pages.flatMap((p) => p.data), [services.data]);
  const saved = layout.data?.targets;
  const lines = live.data?.lines;

  // Traffic changes every few seconds; which nodes and lines there are
  // rarely does. React Flow's graph changes only then, and values reach the
  // nodes through the live store (ADR-0018).
  const serviceIds = useMemo(() => new Set(list?.map((s) => s.id)), [list]);
  const liveTargets = useMemo(() => targetsOf(lines ?? [], serviceIds), [lines, serviceIds]);
  const targets = useStable(liveTargets, JSON.stringify(liveTargets));
  const names = useMemo(() => new Map([...(list ?? []).map((s) => [s.id, s.name] as const), ...targets.map((t) => [t.id, t.name] as const)]), [list, targets]);
  const liveEdges = useMemo(() => canvasEdges(lines ?? [], dependencies.data?.data ?? [], names), [lines, dependencies.data, names]);
  const edges = useStable(liveEdges, liveEdges.map((e) => `${e.id}:${e.ariaLabel}`).join("|"));

  const [store] = useState(() => new LiveMetrics());
  useEffect(() => {
    if (!live.data) return;
    const { nodes, edges } = liveValues(live.data.services, live.data.lines);
    store.update(nodes, edges);
  }, [live.data, store]);

  const [nodes, setNodes] = useState<ServiceNode[]>([]);
  const [save, setSave] = useState<SaveState>({ state: "idle" });
  const [arranging, setArranging] = useState(false);
  const pending = useRef(new Set<string>());
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;
  const edgesRef = useRef(edges);
  edgesRef.current = edges;

  const build = useCallback(
    (current: ServiceNode[]) => {
      const placed = servicesToNodes(list ?? [], current, canEdit);
      return [...placed, ...targetsToNodes(targets, saved ?? [], current, placed, canEdit)];
    },
    [list, targets, saved, canEdit],
  );
  const buildRef = useRef(build);
  buildRef.current = build;

  // Nodes added, renamed or removed elsewhere show up; local moves stay.
  useEffect(() => {
    if (list && (saved || layout.isError)) setNodes(build);
  }, [list, saved, layout.isError, build]);

  // Databases and hosts arrive with the first traffic, after the canvas fit
  // its services, and may be outside the view.
  const fitted = useRef(false);
  useEffect(() => {
    if (fitted.current || targets.length === 0) return;
    fitted.current = true;
    requestAnimationFrame(() => void flow.fitView(FIT));
  }, [targets, flow]);

  // keepalive lets a save finish while the page unloads.
  const flush = useCallback(
    async (keepalive = false) => {
      const ids = [...pending.current];
      pending.current.clear();
      const byId = new Map(nodesRef.current.map((n) => [n.id, n]));
      const moved = ids.flatMap((id) => byId.get(id) ?? []);
      const at = (n: ServiceNode) => ({ x: Math.round(n.position.x), y: Math.round(n.position.y) });
      const positions = moved.filter((n) => n.data.kind === "service").map((n) => ({ serviceId: n.id, ...at(n) }));
      const targetMoves = moved.flatMap((n) => (n.data.kind === "database" || n.data.kind === "external" ? [{ kind: n.data.kind, name: n.data.name, ...at(n) }] : []));
      if (moved.length === 0) return;
      try {
        await unwrap(api.PUT("/api/v1/projects/{projectId}/canvas", { params: { path: { projectId } }, body: { positions, targets: targetMoves }, keepalive }));
        // A move made while this one saved is still on its way.
        if (pending.current.size === 0) setSave({ state: "saved" });
      } catch (err) {
        setSave({ state: "failed", message: describeError(err) });
        // Put the nodes back where they were saved. The refetch below may
        // return the same data, which wouldn't rebuild the nodes by itself.
        setNodes(buildRef.current([]));
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: servicesKey(projectId) }),
        queryClient.invalidateQueries({ queryKey: canvasLayoutKey(projectId) }),
      ]);
    },
    [projectId, queryClient],
  );

  const schedule = useCallback(
    (ids: string[]) => {
      for (const id of ids) pending.current.add(id);
      setSave({ state: "saving" });
      clearTimeout(timer.current);
      timer.current = setTimeout(() => void flush(), SAVE_DELAY_MS);
    },
    [flush],
  );
  // A move made just before leaving the page, or this view, still saves.
  useEffect(() => {
    const now = () => {
      if (pending.current.size > 0) {
        clearTimeout(timer.current);
        void flush(true);
      }
    };
    window.addEventListener("pagehide", now);
    return () => {
      window.removeEventListener("pagehide", now);
      now();
    };
  }, [flush]);

  const onNodesChange = useCallback(
    (changes: NodeChange<ServiceNode>[]) => {
      setNodes((ns) => applyNodeChanges(changes, ns));
      if (canEdit) {
        const moved = finishedMoves(changes);
        if (moved.length > 0) schedule(moved);
      }
    },
    [canEdit, schedule],
  );

  const onConnect = useCallback(
    (c: Connection) => {
      if (c.source !== c.target) declare.mutate({ fromServiceId: c.source, toServiceId: c.target });
    },
    [declare],
  );

  async function arrange() {
    setArranging(true);
    try {
      const positions = await layoutWithElk(nodesRef.current, edgesRef.current);
      const snapped = (v: number) => Math.round(v / GRID[0]) * GRID[0];
      setNodes((ns) =>
        ns.map((n) => {
          const p = positions.get(n.id);
          return p ? { ...n, position: { x: snapped(p.x), y: snapped(p.y) } } : n;
        }),
      );
      schedule([...positions.keys()]);
      // Let React Flow draw the new positions before fitting them.
      requestAnimationFrame(() => void flow.fitView({ ...FIT, duration: 300 }));
    } finally {
      setArranging(false);
    }
  }

  const empty = list !== undefined && list.length === 0;
  const problem =
    save.state === "failed"
      ? `The layout couldn't be saved: ${save.message}`
      : services.isError
        ? `The services couldn't load: ${describeError(services.error)}`
        : declare.isError
          ? `The dependency couldn't be declared: ${describeError(declare.error)}`
          : live.isError
            ? `Live traffic couldn't load: ${describeError(live.error)}`
            : undefined;
  return (
    <div className="canvas">
      <LiveMetricsContext.Provider value={store}>
        <ReactFlow<ServiceNode>
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          onConnect={onConnect}
          onMoveStart={() => store.hold(true)}
          onMoveEnd={() => store.hold(false)}
          onNodeDoubleClick={(_, node) => {
            if (node.data.kind === "service") void navigate({ to: "/services/$serviceId", params: { serviceId: node.id } });
          }}
          nodesDraggable={canEdit}
          // Whole units keep saved positions exact; the arrow keys move a step.
          snapToGrid
          snapGrid={GRID}
          nodesConnectable={canEdit}
          edgesFocusable={false}
          // Nothing is removed from here: services on their page, dependencies from their list.
          deleteKeyCode={null}
          fitView
          fitViewOptions={FIT}
          minZoom={0.2}
          proOptions={{ hideAttribution: true }}
          aria-label="Service canvas"
          colorMode="system"
        >
          <Background variant={BackgroundVariant.Dots} gap={24} size={1.5} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </LiveMetricsContext.Provider>
      {list && list.length > 0 ? (
        <div className="canvas-bar">
          <p className="canvas-hint">
            {canEdit
              ? "Drag services, or select one and use the arrow keys, to arrange them for everyone. Double-click a service to open it."
              : "Double-click a service to open it."}
          </p>
          <p className="canvas-status" role="status">
            {save.state === "saving" ? "Saving…" : save.state === "saved" ? "Layout saved" : ""}
          </p>
          {canEdit && nodes.length > 1 ? (
            <button type="button" className="button secondary small" disabled={arranging} onClick={() => void arrange()}>
              {arranging ? "Arranging…" : "Arrange automatically"}
            </button>
          ) : null}
        </div>
      ) : null}
      {problem ? (
        <p className="canvas-alert form-alert" role="alert">
          {problem}
        </p>
      ) : null}
      {empty ? (
        <div className="canvas-empty">
          <p className="empty-title">No services on this canvas yet</p>
          <p>
            Each service of the project is a node here, showing its spec version, health and latency.
            {canEdit ? " Add one below, then drag it where it belongs." : ""}
          </p>
        </div>
      ) : null}
    </div>
  );
}
