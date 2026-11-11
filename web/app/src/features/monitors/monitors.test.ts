// SPDX-License-Identifier: Apache-2.0

import type { Check, Monitor } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import { assertionRow, chartBars, emptyMonitor, formatInterval, formatSeconds, formatUptime, fromMonitor, heartbeatURL, toCreate, toUpdate, withKind, withMonitorKind } from "./model";
import { keepFreshToken, takeFreshToken } from "./tokens";

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
    expect(body.request?.url).toBe("https://example.com/health");
    expect("serviceId" in body).toBe(false);
  });

  it("round-trips a monitor", () => {
    const body = toCreate(fromMonitor(monitor));
    expect(body.assertions).toEqual(monitor.assertions);
    expect(body.request?.headers).toEqual(monitor.request?.headers);
    expect(body.serviceId).toBe("svc_1");
  });

  it("clears the service on an edit, and never moves the environment", () => {
    const d = fromMonitor(monitor);
    d.serviceId = "";
    const body = toUpdate(d);
    expect(body.serviceId).toBeNull();
    expect("environmentId" in body).toBe(false);
  });

  it("sends a collection monitor's collection and folder, and nothing of a request", () => {
    const d = withMonitorKind({ ...emptyMonitor("env_1"), name: "Smoke", collectionId: "col_1", folderId: "item_1" }, "collection");
    expect(toCreate(d)).toEqual({
      name: "Smoke",
      kind: "collection",
      environmentId: "env_1",
      collectionId: "col_1",
      folderId: "item_1",
      intervalSeconds: 300,
      timeoutSeconds: 60,
      failuresToConfirm: 2,
    });
    // Running the whole collection again clears the folder.
    expect(toUpdate({ ...d, folderId: "" })).toMatchObject({ folderId: null, collectionId: "col_1" });
  });

  it("sends a heartbeat's interval and grace only", () => {
    const d = withMonitorKind({ ...emptyMonitor("env_1"), name: "Backup" }, "heartbeat");
    expect(toCreate(d)).toEqual({ name: "Backup", kind: "heartbeat", environmentId: "env_1", intervalSeconds: 86400, graceSeconds: 3600 });
    const fromAPI = fromMonitor({ ...monitor, kind: "heartbeat", request: undefined, assertions: [], timeoutSeconds: 0, failuresToConfirm: 1, graceSeconds: 300 });
    expect(toUpdate(fromAPI)).toEqual({ name: "Orders", kind: "heartbeat", serviceId: "svc_1", intervalSeconds: 60, graceSeconds: 300 });
  });

  it("keeps a request's settings between HTTP and health checks", () => {
    const d = fromMonitor(monitor);
    expect(withMonitorKind(d, "http")).toEqual({ ...d, kind: "http" });
  });
});

describe("heartbeat tokens", () => {
  it("are shown once, from memory", () => {
    keepFreshToken("mon_1", "sbx_hb_a");
    expect(takeFreshToken("mon_1")).toBe("sbx_hb_a");
    expect(takeFreshToken("mon_1")).toBeUndefined();
    expect(heartbeatURL("https://sb.example.com", "sbx_hb_a")).toBe("https://sb.example.com/api/v1/heartbeats/sbx_hb_a");
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
    expect(formatInterval(86400)).toBe("every day");
    expect(formatInterval(7200)).toBe("every 2 hours");
    expect(formatSeconds(90)).toBe("90 seconds");
    expect(formatSeconds(0)).toBe("0 seconds");
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
