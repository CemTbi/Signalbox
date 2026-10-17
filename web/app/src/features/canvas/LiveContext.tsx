// SPDX-License-Identifier: Apache-2.0

import { createContext, useCallback, useContext, useSyncExternalStore } from "react";
import type { LiveEdge, LiveMetrics, LiveNode } from "./live";

/** The live metrics of the canvas being shown, if it shows any. */
export const LiveMetricsContext = createContext<LiveMetrics | undefined>(undefined);

const never = () => () => {};

function useLive<T>(key: string, read: (store: LiveMetrics) => T): T | undefined {
  const store = useContext(LiveMetricsContext);
  const subscribe = useCallback((l: () => void) => (store ? store.subscribe(key, l) : never()), [store, key]);
  return useSyncExternalStore(store ? subscribe : never, () => (store ? read(store) : undefined));
}

/** The live values of one node, or undefined without live metrics. */
export function useLiveNode(id: string): LiveNode | undefined {
  return useLive(`n:${id}`, (s) => s.node(id));
}

/** One line's live value, or undefined if none came for it. */
export function useLiveEdge(id: string): LiveEdge | undefined {
  return useLive(`e:${id}`, (s) => s.edge(id));
}
