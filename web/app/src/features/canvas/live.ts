// SPDX-License-Identifier: Apache-2.0

import type { ServiceData } from "./model";

/** The values of a node that change while the canvas is open. */
export type LiveNode = Pick<ServiceData, "health" | "p95Ms" | "errorRate" | "rps">;

/**
 * Live metrics for the canvas, kept apart from React Flow's nodes and edges.
 * When metrics arrive, React Flow's graph doesn't change, so it does no
 * work; only the nodes and lines whose values changed re-render, each
 * subscribed to its own ID. ADR-0018 has the measurements behind this.
 */
export class LiveMetrics {
  private nodes = new Map<string, LiveNode>();
  private edges = new Map<string, number | undefined>();
  private listeners = new Map<string, Set<() => void>>();
  private held = new Set<string>();
  private holding = false;

  /** Stores new values and shows those that changed, unless held. */
  update(nodes: Map<string, LiveNode>, edges: Map<string, number | undefined>) {
    const changed: string[] = [];
    for (const [id, v] of nodes) {
      const old = this.nodes.get(id);
      if (!old || old.health !== v.health || old.p95Ms !== v.p95Ms || old.errorRate !== v.errorRate || old.rps !== v.rps) {
        this.nodes.set(id, v);
        changed.push(`n:${id}`);
      }
    }
    for (const [id, v] of edges) {
      if (this.edges.get(id) !== v) {
        this.edges.set(id, v);
        changed.push(`e:${id}`);
      }
    }
    for (const key of changed) {
      if (this.holding) this.held.add(key);
      else this.notify(key);
    }
  }

  /**
   * Holds changes back while someone pans or zooms the canvas, and shows
   * them all when they stop. Re-rendering every node costs a frame or more
   * on slow machines, and a stutter is most noticeable mid-gesture.
   */
  hold(on: boolean) {
    this.holding = on;
    if (on) return;
    for (const key of this.held) this.notify(key);
    this.held.clear();
  }

  private notify(key: string) {
    for (const l of this.listeners.get(key) ?? []) l();
  }

  node(id: string) {
    return this.nodes.get(id);
  }

  edge(id: string) {
    return this.edges.get(id);
  }

  subscribe(key: string, listener: () => void) {
    let set = this.listeners.get(key);
    if (!set) this.listeners.set(key, (set = new Set()));
    set.add(listener);
    return () => {
      set.delete(listener);
    };
  }
}
