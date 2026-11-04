// SPDX-License-Identifier: Apache-2.0

import type { Assertion, Check, Monitor, MonitorCreate, SavedRequest } from "@signalbox/api-client";
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

/** What the monitor form edits. */
export type MonitorDraft = {
  name: string;
  kind: Monitor["kind"];
  environmentId: string;
  serviceId: string;
  request: Draft;
  assertions: AssertionRow[];
  intervalSeconds: number;
  timeoutSeconds: number;
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
    intervalSeconds: 60,
    timeoutSeconds: 10,
    failuresToConfirm: 2,
    certificateWarningDays: 14,
  };
}

export function fromMonitor(m: Monitor): MonitorDraft {
  return {
    name: m.name,
    kind: m.kind,
    environmentId: m.environmentId,
    serviceId: m.serviceId ?? "",
    request: fromSaved(m.request),
    assertions: m.assertions.map(assertionRow),
    intervalSeconds: m.intervalSeconds,
    timeoutSeconds: m.timeoutSeconds,
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
  const { contract: _contract, ...saved } = toSaved(d);
  return saved;
}

export function toCreate(d: MonitorDraft): MonitorCreate {
  return {
    name: d.name,
    kind: d.kind,
    environmentId: d.environmentId,
    ...(d.serviceId ? { serviceId: d.serviceId } : {}),
    request: toRequest(d.request),
    assertions: d.assertions.map(toAssertion),
    intervalSeconds: d.intervalSeconds,
    timeoutSeconds: d.timeoutSeconds,
    failuresToConfirm: d.failuresToConfirm,
    certificateWarningDays: d.certificateWarningDays,
  };
}

/** The fields of an edit. A monitor's environment doesn't change: its results and state belong to it. */
export function toUpdate(d: MonitorDraft) {
  const { environmentId: _environmentId, serviceId, ...rest } = toCreate(d);
  return { ...rest, serviceId: serviceId ?? null };
}

/** How often, in words. */
export function formatInterval(seconds: number): string {
  if (seconds % 3600 === 0) return seconds === 3600 ? "every hour" : `every ${seconds / 3600} hours`;
  if (seconds % 60 === 0) return seconds === 60 ? "every minute" : `every ${seconds / 60} minutes`;
  return `every ${seconds} seconds`;
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
