// SPDX-License-Identifier: Apache-2.0

import type { Check, Monitor } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import { assertionRow, chartBars, emptyMonitor, formatInterval, formatUptime, fromMonitor, toCreate, toUpdate, withKind } from "./model";

const monitor: Monitor = {
  id: "mon_1",
  projectId: "prj_1",
  environmentId: "env_1",
  serviceId: "svc_1",
  name: "Orders",
  kind: "health",
  request: { method: "GET", url: "{{baseUrl}}/health", headers: [{ key: "X-A", value: "1", enabled: true }], body: "" },
  assertions: [{ kind: "jsonPath", target: "$.status", op: "equals", value: "ok" }],
  intervalSeconds: 60,
  timeoutSeconds: 5,
  failuresToConfirm: 2,
  certificateWarningDays: 14,
  state: "up",
  consecutiveFailures: 0,
  nextCheckAt: "2026-11-04T12:01:00Z",
  createdAt: "2026-11-04T12:00:00Z",
  updatedAt: "2026-11-04T12:00:00Z",
};

const check = (over: Partial<Check>): Check => ({ at: "2026-11-04T12:00:00Z", outcome: "passed", durationMs: 100, components: [], ...over });

describe("the monitor form's values", () => {
  it("starts with the defaults", () => {
    expect(emptyMonitor("env_1")).toMatchObject({ environmentId: "env_1", kind: "http", intervalSeconds: 60, timeoutSeconds: 10, failuresToConfirm: 2, certificateWarningDays: 14 });
  });

  it("sends a new monitor as it was edited, leaving out what isn't set", () => {
    const d = emptyMonitor("env_1");
    d.name = "Orders";
    d.request.url = "https://example.com/health";
    const body = toCreate(d);
    expect(body).toMatchObject({ name: "Orders", environmentId: "env_1", assertions: [] });
    expect(body.request.url).toBe("https://example.com/health");
    expect("serviceId" in body).toBe(false);
  });

  it("round-trips a monitor", () => {
    const body = toCreate(fromMonitor(monitor));
    expect(body.assertions).toEqual(monitor.assertions);
    expect(body.request.headers).toEqual(monitor.request.headers);
    expect(body.serviceId).toBe("svc_1");
  });

  it("clears the service on an edit, and never moves the environment", () => {
    const d = fromMonitor(monitor);
    d.serviceId = "";
    const body = toUpdate(d);
    expect(body.serviceId).toBeNull();
    expect("environmentId" in body).toBe(false);
  });
});

describe("assertions", () => {
  it("keep a comparison their new kind takes", () => {
    const row = assertionRow({ kind: "jsonPath", target: "$.a", op: "lessThan", value: "3" });
    expect(withKind(row, "status")).toMatchObject({ op: "lessThan", target: undefined });
    expect(withKind(row, "header")).toMatchObject({ op: "equals", target: "$.a" });
    expect(withKind(row, "responseTime")).toMatchObject({ op: "lessThan" });
  });

  it("send only what their comparison reads", () => {
    const d = emptyMonitor("env_1");
    d.assertions = [assertionRow({ kind: "header", target: "X-A", op: "exists", value: "left over" }), assertionRow({ kind: "status", target: "left over", op: "equals", value: "200" })];
    expect(toCreate(d).assertions).toEqual([
      { kind: "header", target: "X-A", op: "exists" },
      { kind: "status", op: "equals", value: "200" },
    ]);
  });
});

describe("formats", () => {
  it("say how often", () => {
    expect(formatInterval(30)).toBe("every 30 seconds");
    expect(formatInterval(60)).toBe("every minute");
    expect(formatInterval(300)).toBe("every 5 minutes");
    expect(formatInterval(3600)).toBe("every hour");
  });

  it("never round uptime up to 100%", () => {
    expect(formatUptime(1)).toBe("100%");
    expect(formatUptime(0.99999)).toBe("99.99%");
    expect(formatUptime(0.5)).toBe("50.00%");
  });
});

describe("chartBars", () => {
  it("draws oldest first, scaled to the slowest check that ran", () => {
    const bars = chartBars([
      check({ at: "3", durationMs: 50 }),
      check({ at: "2", outcome: "skipped", durationMs: 9999 }),
      check({ at: "1", durationMs: 200, outcome: "failed" }),
    ]);
    expect(bars.map((b) => b.check.at)).toEqual(["1", "2", "3"]);
    expect(bars.map((b) => b.height)).toEqual([1, 0, 0.25]);
  });

  it("keeps the quickest check visible", () => {
    const bars = chartBars([check({ durationMs: 0.1 }), check({ at: "2", durationMs: 1000 })]);
    expect(bars[1]?.height).toBe(0.02);
  });
});
