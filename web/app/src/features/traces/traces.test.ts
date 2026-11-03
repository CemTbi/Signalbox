// SPDX-License-Identifier: Apache-2.0

import type { Span } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import { criticalPath, formatDuration, serviceColors, startMs, waterfall } from "./model";
import { errorRate } from "./ServicesView";
import { validateTracesSearch } from "./TracesPage";

const t0 = Date.parse("2026-10-31T12:00:00Z");

function span(id: string, parent: string | undefined, offsetMs: number, durationMs: number, service = "web"): Span {
  const at = new Date(t0 + Math.floor(offsetMs)).toISOString().replace("Z", "");
  const micros = Math.round((offsetMs % 1) * 1000);
  return {
    spanId: id,
    parentSpanId: parent,
    name: id,
    operation: id,
    kind: "server",
    service,
    start: `${at}${String(micros).padStart(3, "0")}000Z`,
    durationMs,
    status: "unset",
    attributes: {},
    resource: {},
    events: [],
    links: [],
  };
}

describe("startMs", () => {
  it("keeps the microseconds a Date would drop", () => {
    expect(startMs(span("a", undefined, 1.25, 1)) - t0).toBeCloseTo(1.25, 6);
  });
});

describe("waterfall", () => {
  it("orders spans as a tree, children by start, orphans after the root", () => {
    const spans = [
      span("c2", "root", 50, 10),
      span("orphan", "gone", 5, 3),
      span("root", undefined, 0, 100),
      span("c1", "root", 10, 30),
      span("g1", "c1", 12, 5),
    ];
    const rows = waterfall(spans);
    expect(rows.map((r) => [r.span.spanId, r.depth])).toEqual([
      ["root", 0],
      ["c1", 1],
      ["g1", 2],
      ["c2", 1],
      ["orphan", 0],
    ]);
    expect(rows[4]?.orphan).toBe(true);
    expect(rows[2]?.offsetMs).toBe(12);
    expect(rows[3]?.endMs).toBe(60);
  });

  it("survives a span that names itself as its parent", () => {
    expect(waterfall([span("a", "a", 0, 1)]).map((r) => r.span.spanId)).toEqual(["a"]);
  });
});

describe("criticalPath", () => {
  it("follows what the root waited for, back to its start", () => {
    // root: 0–100. a (10–40) then b (45–95) run one after the other; c
    // (50–60) runs beside b and isn't waited for.
    const spans = [
      span("root", undefined, 0, 100),
      span("a", "root", 10, 30),
      span("b", "root", 45, 50),
      span("c", "root", 50, 10),
      span("b1", "b", 46, 40),
    ];
    expect([...criticalPath(spans)].sort()).toEqual(["a", "b", "b1", "root"]);
  });

  it("is the root alone without children", () => {
    expect([...criticalPath([span("root", undefined, 0, 5)])]).toEqual(["root"]);
  });
});

describe("serviceColors", () => {
  it("gives each service a color, by name order", () => {
    const colors = serviceColors([span("a", undefined, 0, 1, "web"), span("b", "a", 0, 1, "api"), span("c", "a", 0, 1, "web")]);
    expect([...colors]).toEqual([
      ["api", "var(--s1)"],
      ["web", "var(--s2)"],
    ]);
  });
});

describe("formatDuration", () => {
  it("reads naturally at every scale", () => {
    expect(formatDuration(0.25)).toBe("250 µs");
    expect(formatDuration(4.5)).toBe("4.50 ms");
    expect(formatDuration(42.25)).toBe("42.3 ms");
    expect(formatDuration(312)).toBe("312 ms");
    expect(formatDuration(1500)).toBe("1.50 s");
    expect(formatDuration(65000)).toBe("65.0 s");
  });
});

describe("validateTracesSearch", () => {
  it("keeps what's valid and drops the rest", () => {
    expect(validateTracesSearch({ range: "6h", service: "web", errors: "true", minMs: "250", attribute: "", operation: 3 })).toEqual({
      range: "6h",
      service: "web",
      errors: true,
      minMs: 250,
    });
    expect(validateTracesSearch({ range: "forever", minMs: "-1", errors: "no" })).toEqual({});
  });
});

describe("errorRate", () => {
  it("reads as a percentage, never rounding failures away", () => {
    expect(errorRate(0, 0)).toBe("0%");
    expect(errorRate(100, 0)).toBe("0%");
    expect(errorRate(10_000, 1)).toBe("<0.1%");
    expect(errorRate(1000, 25)).toBe("2.5%");
    expect(errorRate(100, 37)).toBe("37%");
  });
});
