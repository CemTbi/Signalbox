// SPDX-License-Identifier: Apache-2.0

import type { Dependency, LiveLine, LiveService, Service, TargetPlacement } from "@signalbox/api-client";
import { MarkerType, type NodeChange } from "@xyflow/react";
import type { LiveEdge, LiveNode } from "../canvas/live";
import { type CallEdge, NODE_HEIGHT, NODE_WIDTH, type ServiceNode } from "../canvas/model";

type Point = { x: number; y: number };

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
      connectable: draggable,
      data: { name: s.name, kind: "service", health: "unknown" },
    };
  });
}

/** A database or outside host that traffic shows a service on the canvas calling. */
export type Target = { id: string; kind: "database" | "external"; name: string; system?: string; callers: string[] };

/** The databases and hosts called from nodes on the canvas, in a stable order. */
export function targetsOf(lines: LiveLine[], onCanvas: Set<string>): Target[] {
  const byId = new Map<string, Target>();
  for (const l of lines) {
    if (l.toKind === "service" || !onCanvas.has(l.from)) continue;
    const t = byId.get(l.to) ?? { id: l.to, kind: l.toKind, name: l.toName, system: l.system, callers: [] };
    t.callers.push(l.from);
    t.callers.sort();
    byId.set(l.to, t);
  }
  return [...byId.values()].sort((a, b) => a.id.localeCompare(b.id));
}

/** Room left around a node placed automatically, for its lines. */
const GAP_X = 96;
const GAP_Y = 28;
const STEP = 8;

const snapped = (v: number) => Math.round(v / STEP) * STEP;

function overlaps(a: Point, b: Point) {
  return Math.abs(a.x - b.x) < NODE_WIDTH + GAP_X / 2 && Math.abs(a.y - b.y) < NODE_HEIGHT + GAP_Y;
}

/**
 * A place for a new database or host: to the right of what calls it, level
 * with it, and below anything already there. A layout of the whole canvas
 * would give a better picture, but would move what people placed by hand;
 * Arrange automatically does that when someone asks.
 */
export function placeNear(callers: Point[], taken: Point[]): Point {
  const x = callers.length > 0 ? Math.max(...callers.map((c) => c.x)) + NODE_WIDTH + GAP_X : 0;
  const y = callers.length > 0 ? callers.reduce((sum, c) => sum + c.y, 0) / callers.length : 0;
  const spot = { x: snapped(x), y: snapped(y) };
  while (taken.some((t) => overlaps(spot, t))) spot.y += snapped(NODE_HEIGHT + GAP_Y);
  return spot;
}

/**
 * The canvas's nodes for databases and hosts. Each stays where it is on the
 * canvas, or goes where someone saved it, or is placed near what calls it.
 */
export function targetsToNodes(targets: Target[], saved: TargetPlacement[], current: ServiceNode[], others: ServiceNode[], draggable: boolean): ServiceNode[] {
  const here = new Map(current.map((n) => [n.id, n]));
  const placed = new Map(saved.map((t) => [`${t.kind}:${t.name}`, { x: t.x, y: t.y }]));
  const at = new Map(others.map((n) => [n.id, n.position]));
  const taken = [...at.values(), ...placed.values()];
  return targets.map((t) => {
    const prev = here.get(t.id);
    let position = prev?.position ?? placed.get(t.id);
    if (!position) {
      position = placeNear(t.callers.flatMap((c) => at.get(c) ?? []), taken);
      taken.push(position);
    }
    return {
      id: t.id,
      type: "service",
      position,
      selected: prev?.selected,
      draggable,
      connectable: false,
      data: { name: t.name, kind: t.kind, system: t.system, health: "unknown" },
    };
  });
}

/**
 * The lines to draw: those traffic shows, solid, and those people declared
 * that traffic doesn't show yet, dashed. Both are named by their two nodes,
 * so traffic takes a declared line over.
 */
export function canvasEdges(lines: LiveLine[], dependencies: Dependency[], names: Map<string, string>): CallEdge[] {
  const edges = new Map<string, CallEdge>();
  const add = (id: string, source: string, target: string, declared: boolean) => {
    const from = names.get(source);
    const to = names.get(target);
    if (from === undefined || to === undefined || edges.has(id)) return;
    edges.set(id, {
      id,
      source,
      target,
      type: "call",
      selectable: false,
      ariaLabel: declared ? `${from} is declared to call ${to}` : `${from} calls ${to}`,
      markerEnd: { type: MarkerType.ArrowClosed },
      data: { declared },
    });
  };
  for (const l of lines) add(l.id, l.from, l.to, false);
  for (const d of dependencies) add(`${d.fromServiceId}>${d.toServiceId}`, d.fromServiceId, d.toServiceId, true);
  return [...edges.values()].sort((a, b) => a.id.localeCompare(b.id));
}

/** What the live store should show of traffic: nodes by ID, lines by ID. */
export function liveValues(services: LiveService[], lines: LiveLine[]) {
  const nodes = new Map<string, LiveNode>();
  for (const s of services) {
    nodes.set(s.id, { health: s.health, healthDetail: s.healthDetail, rps: s.rate, errorRate: s.errorRate, p95Ms: s.p95Ms });
  }
  const edges = new Map<string, LiveEdge>();
  // A database or host has the calls to it, added up.
  const calls = new Map<string, { rate: number; failing: number }>();
  for (const l of lines) {
    edges.set(l.id, { rps: l.rate, errorRate: l.errorRate });
    if (l.toKind === "service") continue;
    const c = calls.get(l.to) ?? { rate: 0, failing: 0 };
    c.rate += l.rate;
    c.failing += l.rate * l.errorRate;
    calls.set(l.to, c);
  }
  for (const [id, c] of calls) {
    nodes.set(id, {
      health: "unknown",
      rps: Number(c.rate.toPrecision(3)),
      errorRate: c.rate > 0 ? Number((c.failing / c.rate).toFixed(4)) : 0,
      p95Ms: undefined,
    });
  }
  return { nodes, edges };
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

/** The environment to show: the one chosen if it still exists, or production, where traffic usually is. */
export function chooseEnvironment(environments: { id: string; kind: string }[], chosen: string): string {
  if (environments.some((e) => e.id === chosen)) return chosen;
  return environments.find((e) => e.kind === "production")?.id ?? environments[0]?.id ?? "";
}

/** The environment someone last watched a project's canvas in, kept in their browser. */
export function rememberedCanvasEnvironment(projectId: string): string {
  try {
    return localStorage.getItem(`signalbox.canvas.environment.${projectId}`) ?? "";
  } catch {
    return "";
  }
}

/** Remembers the environment chosen for a canvas, for the next visit. */
export function rememberCanvasEnvironment(projectId: string, environmentId: string) {
  try {
    localStorage.setItem(`signalbox.canvas.environment.${projectId}`, environmentId);
  } catch {
    // Remembering is a convenience; without storage the choice lasts the visit.
  }
}
