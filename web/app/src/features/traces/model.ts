// SPDX-License-Identifier: Apache-2.0

import type { Span } from "@signalbox/api-client";

/** A span placed in its trace's waterfall. */
export interface Row {
  span: Span;
  depth: number;
  /** Start and end, in milliseconds from the trace's start. */
  offsetMs: number;
  endMs: number;
  /** The span names a parent that hasn't arrived. */
  orphan: boolean;
}

/** The time a span starts, in milliseconds since the epoch, to the microsecond. */
export function startMs(s: Span): number {
  // Date keeps milliseconds only; the fraction past them comes from the text.
  const [whole, fraction = ""] = s.start.replace("Z", "").split(".");
  const ms = Date.parse(`${whole}Z`);
  return ms + Number(`0.${fraction.padEnd(9, "0")}`) * 1000;
}

/**
 * Orders a trace's spans as a tree, each child under its parent by start
 * time. Spans whose parent hasn't arrived start trees of their own, after
 * the root.
 */
export function waterfall(spans: Span[]): Row[] {
  if (spans.length === 0) return [];
  const byId = new Map(spans.map((s) => [s.spanId, s]));
  const children = new Map<string, Span[]>();
  const tops: Span[] = [];
  for (const s of spans) {
    if (s.parentSpanId && byId.has(s.parentSpanId) && s.parentSpanId !== s.spanId) {
      const list = children.get(s.parentSpanId) ?? [];
      list.push(s);
      children.set(s.parentSpanId, list);
    } else {
      tops.push(s);
    }
  }
  const byStart = (a: Span, b: Span) => startMs(a) - startMs(b);
  // The true root first, then spans whose parents are missing.
  tops.sort((a, b) => Number(Boolean(a.parentSpanId)) - Number(Boolean(b.parentSpanId)) || byStart(a, b));
  const t0 = Math.min(...spans.map(startMs));
  const rows: Row[] = [];
  const seen = new Set<string>();
  const visit = (s: Span, depth: number) => {
    if (seen.has(s.spanId)) return; // a cycle from a broken exporter
    seen.add(s.spanId);
    const offsetMs = startMs(s) - t0;
    rows.push({ span: s, depth, offsetMs, endMs: offsetMs + s.durationMs, orphan: Boolean(s.parentSpanId) && !byId.has(s.parentSpanId!) });
    for (const c of (children.get(s.spanId) ?? []).sort(byStart)) visit(c, depth + 1);
  };
  for (const s of tops) visit(s, 0);
  return rows;
}

/**
 * The spans on a trace's critical path: the work the trace waited for.
 * From a span, it follows the child that finished last, then the children
 * that finished before that one started, back to the span's start, as
 * Jaeger's critical path does. Time a span spent itself is on the path too.
 */
export function criticalPath(spans: Span[]): Set<string> {
  const path = new Set<string>();
  const ids = new Set(spans.map((s) => s.spanId));
  const children = new Map<string, Span[]>();
  for (const s of spans) {
    if (s.parentSpanId && ids.has(s.parentSpanId)) {
      const list = children.get(s.parentSpanId) ?? [];
      list.push(s);
      children.set(s.parentSpanId, list);
    }
  }
  const end = (s: Span) => startMs(s) + s.durationMs;
  const walk = (s: Span, until: number) => {
    if (path.has(s.spanId)) return;
    path.add(s.spanId);
    // Walking back from the end: the child that finished last is what the
    // span waited for; before it started, the one that finished last
    // before then; and so on. Children that started after the cursor ran
    // beside one already on the path.
    const kids = (children.get(s.spanId) ?? []).sort((a, b) => end(b) - end(a));
    let cursor = Math.min(end(s), until);
    for (const c of kids) {
      if (startMs(c) >= cursor) continue;
      walk(c, cursor);
      cursor = startMs(c);
      if (cursor <= startMs(s)) break;
    }
  };
  const roots = spans.filter((s) => !s.parentSpanId || !ids.has(s.parentSpanId));
  const first = roots.sort((a, b) => Number(Boolean(a.parentSpanId)) - Number(Boolean(b.parentSpanId)) || startMs(a) - startMs(b))[0];
  if (first) walk(first, end(first));
  return path;
}

/** Colors for services, each with a light and a dark shade, told apart by hue. */
const palette = ["--s1", "--s2", "--s3", "--s4", "--s5", "--s6", "--s7", "--s8"];

/** Gives each service of a trace one of the palette's colors, by name order. */
export function serviceColors(spans: Span[]): Map<string, string> {
  const names = [...new Set(spans.map((s) => s.service))].sort();
  return new Map(names.map((n, i) => [n, `var(${palette[i % palette.length]})`]));
}

/** Formats milliseconds for a waterfall: µs below a millisecond, s above a second. */
export function formatDuration(ms: number): string {
  if (ms < 1) return `${Math.round(ms * 1000)} µs`;
  if (ms < 1000) return `${ms < 10 ? ms.toFixed(2) : ms < 100 ? ms.toFixed(1) : Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`;
}

/** The time ranges a search offers, newest first. */
export const ranges = [
  { value: "15m", label: "Last 15 minutes", ms: 15 * 60_000 },
  { value: "1h", label: "Last hour", ms: 3_600_000 },
  { value: "6h", label: "Last 6 hours", ms: 6 * 3_600_000 },
  { value: "24h", label: "Last 24 hours", ms: 24 * 3_600_000 },
  { value: "7d", label: "Last 7 days", ms: 7 * 24 * 3_600_000 },
] as const;
