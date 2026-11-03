// SPDX-License-Identifier: Apache-2.0

import type { CanvasLive, LiveLine, LiveService } from "@signalbox/api-client";

/** Everything a canvas knows of an environment's traffic, and the ETag of it. */
export type LiveState = { tag: string; services: LiveService[]; lines: LiveLine[] };

function merged<T extends { id: string }>(old: T[], changed: T[], removed: string[]): T[] {
  const byId = new Map(old.map((v) => [v.id, v]));
  for (const id of removed) byId.delete(id);
  for (const v of changed) byId.set(v.id, v);
  return [...byId.values()];
}

/**
 * The state after an answer: its changes applied, or the answer itself when
 * it holds everything. Changes to any other state than this one can't be
 * applied, so the tag is dropped and the next poll gets everything.
 */
export function applyLive(last: LiveState | undefined, answer: CanvasLive, tag: string): LiveState {
  if (answer.changesSince === undefined) return { tag, services: answer.services, lines: answer.lines };
  if (last?.tag !== answer.changesSince) return { tag: "", services: last?.services ?? [], lines: last?.lines ?? [] };
  return {
    tag,
    services: merged(last.services, answer.services, answer.removed.services),
    lines: merged(last.lines, answer.lines, answer.removed.lines),
  };
}
