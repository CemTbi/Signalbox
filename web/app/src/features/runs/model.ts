// SPDX-License-Identifier: Apache-2.0

import type { Run, RunStep } from "@signalbox/api-client";

/** The largest data file a run takes. */
export const maxDataBytes = 5 * 1024 * 1024;

/** A data file's format, from its name: JSON for .json, CSV otherwise. */
export function dataFormat(fileName: string): "csv" | "json" {
  return fileName.toLowerCase().endsWith(".json") ? "json" : "csv";
}

const statusLabels: Record<Run["status"], string> = {
  queued: "Queued",
  running: "Running",
  passed: "Passed",
  failed: "Failed",
  canceled: "Canceled",
  interrupted: "Interrupted",
};

export function statusLabel(status: Run["status"]): string {
  return statusLabels[status];
}

const stoppedReasons: Record<NonNullable<Run["stopped"]>, string> = {
  bail: "It stopped at the first failure, as asked.",
  steps: "It stopped at 10,000 requests, the most a run sends.",
  time: "It stopped after an hour, the longest a run lasts.",
  error: "It stopped on a server error. The server's log says more.",
  restart: "The server stopped during the run.",
};

/** Why a run ended before its last request, in a sentence. */
export function stoppedReason(stopped: Run["stopped"]): string | null {
  return stopped ? stoppedReasons[stopped] : null;
}

/** A step's state, for its badge. */
export function stepState(s: RunStep): "passed" | "failed" | "skipped" {
  if (s.failed) return "failed";
  if (s.failure) return "skipped";
  return "passed";
}

/** The tests a step ran, passed and counted, not counting skipped ones. */
export function stepTests(s: RunStep): { passed: number; total: number } {
  const tests = (s.scripts?.tests ?? []).filter((t) => !t.skipped);
  return { passed: tests.filter((t) => t.passed).length, total: tests.length };
}

/** Where a report of a run downloads from. */
export function reportUrl(runId: string, format: "junit" | "json" | "html"): string {
  return `/api/v1/runs/${encodeURIComponent(runId)}/report?format=${format}`;
}

/** Adds a page of steps after those already shown, skipping any seen. */
export function appendSteps(shown: RunStep[], page: RunStep[]): RunStep[] {
  const last = shown.at(-1)?.seq ?? 0;
  const fresh = page.filter((s) => s.seq > last);
  return fresh.length ? [...shown, ...fresh] : shown;
}
