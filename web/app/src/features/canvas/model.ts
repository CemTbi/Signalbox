// SPDX-License-Identifier: Apache-2.0

import type { Edge, Node } from "@xyflow/react";

/** What a node on the canvas stands for. */
export type ServiceKind = "service" | "database" | "external" | "mock";

/** A service's health, from its monitors. */
export type Health = "ok" | "warn" | "down" | "unknown";

/** What a service node shows. Metrics are missing until traffic is seen. */
export type ServiceData = {
  name: string;
  kind: ServiceKind;
  specVersion?: string;
  health: Health;
  p95Ms?: number;
  errorRate?: number; // 0..1
  rps?: number;
};

/** What a line between services shows: calls per second, from traces. */
export type CallData = {
  rps?: number;
  /** Declared by the team, not seen in traces yet. */
  declared?: boolean;
};

export type ServiceNode = Node<ServiceData, "service">;
export type CallEdge = Edge<CallData>;

/** Node size used for layout. ServiceNode renders at this size. */
export const NODE_WIDTH = 224;
export const NODE_HEIGHT = 92;
