// SPDX-License-Identifier: Apache-2.0

import type { Incident, IncidentEvent } from "@signalbox/api-client";

/** Where an incident stands, in words: acknowledged is an open incident someone is on. */
export function incidentStatus(i: Incident): { label: string; mark: "down" | "warn" | "ok" } {
  if (i.state === "resolved") return { label: "Resolved", mark: "ok" };
  if (i.acknowledgedAt) return { label: "Acknowledged", mark: "warn" };
  return { label: "Open", mark: "down" };
}

export const resolutionLabels: Record<NonNullable<Incident["resolution"]>, string> = {
  recovered: "The monitor recovered",
  manual: "Resolved by hand",
  deleted: "The monitor was deleted",
};

/** How long an incident lasted, or has lasted so far: 45 s, 12 min, 3 h 5 min, 2 d 4 h. */
export function formatDuration(fromISO: string, toISO: string | undefined, now: number = Date.now()): string {
  const seconds = Math.max(0, Math.round(((toISO ? Date.parse(toISO) : now) - Date.parse(fromISO)) / 1000));
  if (seconds < 60) return `${seconds} s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return minutes % 60 ? `${hours} h ${minutes % 60} min` : `${hours} h`;
  const days = Math.floor(hours / 24);
  return hours % 24 ? `${days} d ${hours % 24} h` : `${days} d`;
}

const stateWords: Record<string, string> = { up: "up", degraded: "degraded", down: "down", paused: "paused", pending: "pending" };

/** A line of the timeline in words, without who did it. */
export function describeEvent(e: IncidentEvent): string {
  switch (e.kind) {
    case "opened":
      return "Opened: the monitor went down.";
    case "state":
      return `The monitor went from ${stateWords[e.from ?? ""] ?? e.from} to ${stateWords[e.to ?? ""] ?? e.to}.`;
    case "acknowledged":
      return "Acknowledged.";
    case "resolved":
      return e.detail ?? "Resolved.";
    case "paused":
      return "The monitor was paused.";
    case "resumed":
      return "The monitor was resumed.";
  }
}
