// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { applyNodeChanges, Background, BackgroundVariant, Controls, type NodeChange, ReactFlow, useReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { layoutWithElk } from "../canvas/layout";
import type { ServiceNode } from "../canvas/model";
import { nodeTypes } from "../canvas/ServiceNode";
import { servicesKey, servicesQuery } from "../../queries";
import { finishedMoves, servicesToNodes } from "./placement";

const GRID: [number, number] = [8, 8];

/** How long the canvas waits after a move before saving, so a run of arrow-key moves saves once. */
const SAVE_DELAY_MS = 400;

type SaveState = { state: "idle" } | { state: "saving" } | { state: "saved" } | { state: "failed"; message: string };

/**
 * A project's canvas: each service is a node at the place someone put it.
 * Editors move nodes by dragging them, or by selecting one and using the
 * arrow keys, and the canvas saves where they land for everyone.
 */
export default function Canvas({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  return (
    <ReactFlowProvider>
      <ProjectCanvas projectId={projectId} canEdit={canEdit} />
    </ReactFlowProvider>
  );
}

function ProjectCanvas({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const flow = useReactFlow<ServiceNode>();
  const services = useInfiniteQuery(servicesQuery(api, projectId));
  // A new array only when the services change, so the effect below runs then.
  const list = useMemo(() => services.data?.pages.flatMap((p) => p.data), [services.data]);
  const [nodes, setNodes] = useState<ServiceNode[]>([]);
  const [save, setSave] = useState<SaveState>({ state: "idle" });
  const [arranging, setArranging] = useState(false);
  const pending = useRef(new Set<string>());
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;

  // Services added, renamed or removed elsewhere show up; local moves stay.
  useEffect(() => {
    if (list) setNodes((current) => servicesToNodes(list, current, canEdit));
  }, [list, canEdit]);

  // keepalive lets a save finish while the page unloads.
  const flush = useCallback(async (keepalive = false) => {
    const ids = [...pending.current];
    pending.current.clear();
    const byId = new Map(nodesRef.current.map((n) => [n.id, n]));
    const positions = ids.flatMap((id) => {
      const n = byId.get(id);
      return n ? [{ serviceId: id, x: Math.round(n.position.x), y: Math.round(n.position.y) }] : [];
    });
    if (positions.length === 0) return;
    try {
      await unwrap(api.PUT("/api/v1/projects/{projectId}/canvas", { params: { path: { projectId } }, body: { positions }, keepalive }));
      // A move made while this one saved is still on its way.
      if (pending.current.size === 0) setSave({ state: "saved" });
    } catch (err) {
      setSave({ state: "failed", message: describeError(err) });
      // Show where the services really are.
      setNodes([]);
    }
    await queryClient.invalidateQueries({ queryKey: servicesKey(projectId) });
  }, [projectId, queryClient]);

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

  async function arrange() {
    setArranging(true);
    try {
      const positions = await layoutWithElk(nodesRef.current, []);
      const snapped = (v: number) => Math.round(v / GRID[0]) * GRID[0];
      setNodes((ns) =>
        ns.map((n) => {
          const p = positions.get(n.id);
          return p ? { ...n, position: { x: snapped(p.x), y: snapped(p.y) } } : n;
        }),
      );
      schedule([...positions.keys()]);
      // Let React Flow draw the new positions before fitting them.
      requestAnimationFrame(() => void flow.fitView({ duration: 300 }));
    } finally {
      setArranging(false);
    }
  }

  const empty = list !== undefined && list.length === 0;
  return (
    <div className="canvas">
      <ReactFlow<ServiceNode>
        nodes={nodes}
        edges={[]}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeDoubleClick={(_, node) => void navigate({ to: "/services/$serviceId", params: { serviceId: node.id } })}
        nodesDraggable={canEdit}
        // Whole units keep saved positions exact; the arrow keys move a step.
        snapToGrid
        snapGrid={GRID}
        nodesConnectable={false}
        fitView
        fitViewOptions={{ maxZoom: 1 }}
        minZoom={0.2}
        proOptions={{ hideAttribution: true }}
        aria-label="Service canvas"
        colorMode="system"
      >
        <Background variant={BackgroundVariant.Dots} gap={24} size={1.5} />
        <Controls showInteractive={false} />
      </ReactFlow>
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
          {canEdit && list.length > 1 ? (
            <button type="button" className="button secondary small" disabled={arranging} onClick={() => void arrange()}>
              {arranging ? "Arranging…" : "Arrange automatically"}
            </button>
          ) : null}
        </div>
      ) : null}
      {save.state === "failed" ? (
        <p className="canvas-alert form-alert" role="alert">
          The layout couldn't be saved: {save.message}
        </p>
      ) : null}
      {services.isError ? (
        <p className="canvas-alert form-alert" role="alert">
          The services couldn't load: {describeError(services.error)}
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
