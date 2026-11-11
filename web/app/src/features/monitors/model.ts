// SPDX-License-Identifier: Apache-2.0

import type { Assertion, Check, Monitor, MonitorCreate, MonitorUpdate, SavedRequest } from "@signalbox/api-client";
import { type Draft, emptyDraft, fromSaved, toSaved } from "../requests/model";

/** A monitor's state in words. The shape beside it comes from the same names as the canvas's. */
export const stateLabels: Record<Monitor["state"], string> = {
  pending: "Pending",
  up: "Up",
  degraded: "Degraded",
  down: "Down",
  paused: "Paused",
};

/** The health shape that goes with a state. */
export const stateMarks: Record<Monitor["state"], "ok" | "warn" | "down" | "unknown"> = {
  pending: "unknown",
  up: "ok",
  degraded: "warn",
  down: "down",
  paused: "unknown",
};

export const outcomeLabels: Record<Check["outcome"], string> = {
  passed: "Passed",
  degraded: "Degraded",
  failed: "Failed",
  skipped: "Skipped",
};

export const outcomeMarks: Record<Check["outcome"], "ok" | "warn" | "down" | "unknown"> = {
  passed: "ok",
  degraded: "warn",
  failed: "down",
  skipped: "unknown",
};

export const assertionKinds: { value: Assertion["kind"]; label: string }[] = [
  { value: "status", label: "Status" },
  { value: "header", label: "Header" },
  { value: "jsonPath", label: "JSONPath" },
  { value: "responseTime", label: "Response time (ms)" },
];

/** Which comparisons each kind of assertion takes, as the API does. */
export const opsOf: Record<Assertion["kind"], Assertion["op"][]> = {
  status: ["equals", "notEquals", "lessThan", "greaterThan"],
  header: ["equals", "notEquals", "contains", "exists", "notExists"],
  jsonPath: ["equals", "notEquals", "contains", "lessThan", "greaterThan", "exists", "notExists"],
  responseTime: ["lessThan"],
};

export const opLabels: Record<Assertion["op"], string> = {
  equals: "equals",
  notEquals: "doesn't equal",
  contains: "contains",
  lessThan: "is less than",
  greaterThan: "is greater than",
  exists: "exists",
  notExists: "doesn't exist",
};

/** Whether a comparison needs a value to compare with. */
export const takesValue = (op: Assertion["op"]) => op !== "exists" && op !== "notExists";

/** Whether a kind of assertion names what it reads. */
export const takesTarget = (kind: Assertion["kind"]) => kind === "header" || kind === "jsonPath";

let nextRow = 1;

/** An assertion as the form edits it. */
export type AssertionRow = Assertion & { id: number };

export function assertionRow(a: Assertion = { kind: "status", op: "equals", value: "200" }): AssertionRow {
  return { ...a, id: nextRow++ };
}

/** A row after its kind changes: the comparison has to be one the kind takes. */
export function withKind(row: AssertionRow, kind: Assertion["kind"]): AssertionRow {
  const ops = opsOf[kind];
  const op = ops.includes(row.op) ? row.op : (ops[0] as Assertion["op"]);
  return { ...row, kind, op, target: takesTarget(kind) ? row.target : undefined };
}

export const kindLabels: Record<Monitor["kind"], string> = {
  http: "HTTP check",
  health: "Health check",
  collection: "Collection run",
  heartbeat: "Heartbeat",
};

/** Whether a kind of monitor sends a request of its own. A kind changes only between these. */
export const sendsRequest = (kind: Monitor["kind"]) => kind === "http" || kind === "health";

/** What the monitor form edits. Each kind uses its own fields of it. */
export type MonitorDraft = {
  name: string;
  kind: Monitor["kind"];
  environmentId: string;
  serviceId: string;
  request: Draft;
  assertions: AssertionRow[];
  collectionId: string;
  /** "" runs the whole collection. */
  folderId: string;
  intervalSeconds: number;
  timeoutSeconds: number;
  graceSeconds: number;
  failuresToConfirm: number;
  certificateWarningDays: number;
};

export function emptyMonitor(environmentId: string): MonitorDraft {
  return {
    name: "",
    kind: "http",
    environmentId,
    serviceId: "",
    request: emptyDraft(),
    assertions: [],
    collectionId: "",
    folderId: "",
    intervalSeconds: 60,
    timeoutSeconds: 10,
    graceSeconds: 3600,
    failuresToConfirm: 2,
    certificateWarningDays: 14,
  };
}

/** A new monitor's draft as another kind, with that kind's defaults where they differ, as the API has them. */
export function withMonitorKind(d: MonitorDraft, kind: Monitor["kind"]): MonitorDraft {
  if (sendsRequest(kind) && sendsRequest(d.kind)) return { ...d, kind };
  switch (kind) {
    case "collection":
      return { ...d, kind, intervalSeconds: 300, timeoutSeconds: 60, failuresToConfirm: 2 };
    case "heartbeat":
      return { ...d, kind, intervalSeconds: 86400, graceSeconds: 3600, failuresToConfirm: 1 };
    default:
      return { ...d, kind, intervalSeconds: 60, timeoutSeconds: 10, failuresToConfirm: 2 };
  }
}

export function fromMonitor(m: Monitor): MonitorDraft {
  return {
    name: m.name,
    kind: m.kind,
    environmentId: m.environmentId,
    serviceId: m.serviceId ?? "",
    request: m.request ? fromSaved(m.request) : emptyDraft(),
    assertions: m.assertions.map(assertionRow),
    collectionId: m.collectionId ?? "",
    folderId: m.folderId ?? "",
    intervalSeconds: m.intervalSeconds,
    timeoutSeconds: m.timeoutSeconds,
    graceSeconds: m.graceSeconds ?? 3600,
    failuresToConfirm: m.failuresToConfirm,
    certificateWarningDays: m.certificateWarningDays,
  };
}

function toAssertion({ kind, op, target, value }: AssertionRow): Assertion {
  return {
    kind,
    op,
    ...(takesTarget(kind) ? { target: target ?? "" } : {}),
    ...(takesValue(op) ? { value: value ?? "" } : {}),
  };
}

/** The request a monitor keeps: as the editor's, without scripts, which monitors don't run. */
function toRequest(d: Draft): SavedRequest {
  const saved = toSaved(d);
  delete saved.contract;
  return saved;
}

/** The fields of a kind of monitor, as the API takes them: each kind refuses the others'. */
function kindFields(d: MonitorDraft) {
  switch (d.kind) {
    case "collection":
      return { collectionId: d.collectionId, intervalSeconds: d.intervalSeconds, timeoutSeconds: d.timeoutSeconds, failuresToConfirm: d.failuresToConfirm };
    case "heartbeat":
      return { intervalSeconds: d.intervalSeconds, graceSeconds: d.graceSeconds };
    default:
      return {
        request: toRequest(d.request),
        assertions: d.assertions.map(toAssertion),
        intervalSeconds: d.intervalSeconds,
        timeoutSeconds: d.timeoutSeconds,
        failuresToConfirm: d.failuresToConfirm,
        certificateWarningDays: d.certificateWarningDays,
      };
  }
}

export function toCreate(d: MonitorDraft): MonitorCreate {
  return {
    name: d.name,
    kind: d.kind,
    environmentId: d.environmentId,
    ...(d.serviceId ? { serviceId: d.serviceId } : {}),
    ...kindFields(d),
    ...(d.kind === "collection" && d.folderId ? { folderId: d.folderId } : {}),
  };
}

/** The fields of an edit. A monitor's environment doesn't change: its results and state belong to it. */
export function toUpdate(d: MonitorDraft): MonitorUpdate {
  return {
    name: d.name,
    kind: d.kind,
    serviceId: d.serviceId || null,
    ...kindFields(d),
    ...(d.kind === "collection" ? { folderId: d.folderId || null } : {}),
  };
}

/** A span of seconds in words: 90 seconds, 5 minutes, 2 hours, 1 day. */
export function formatSeconds(seconds: number): string {
  const unit = (n: number, name: string) => `${n} ${name}${n === 1 ? "" : "s"}`;
  if (seconds > 0 && seconds % 86400 === 0) return unit(seconds / 86400, "day");
  if (seconds > 0 && seconds % 3600 === 0) return unit(seconds / 3600, "hour");
  if (seconds > 0 && seconds % 60 === 0) return unit(seconds / 60, "minute");
  return unit(seconds, "second");
}

/** How often, in words. */
export function formatInterval(seconds: number): string {
  const every = formatSeconds(seconds);
  return every.startsWith("1 ") ? `every ${every.slice(2)}` : `every ${every}`;
}

/** The URL a heartbeat's job calls. */
export function heartbeatURL(origin: string, token: string): string {
  return `${origin}/api/v1/heartbeats/${token}`;
}

/** A share as uptime, keeping enough digits that 99.99% isn't 100%. */
export function formatUptime(share: number): string {
  if (share >= 1) return "100%";
  return `${(Math.floor(share * 10000) / 100).toFixed(2)}%`;
}

/** What the response-time chart draws, oldest first: a bar's height is its share of the slowest. */
export function chartBars(checks: Check[]): { check: Check; height: number }[] {
  const ran = checks.filter((c) => c.outcome !== "skipped");
  const slowest = Math.max(1, ...ran.map((c) => c.durationMs));
  return [...checks].reverse().map((check) => ({
    check,
    // A check too quick to see at this scale, or one that took no time at
    // all, still shows.
    height: check.outcome === "skipped" ? 0 : Math.max(0.02, check.durationMs / slowest),
  }));
}
