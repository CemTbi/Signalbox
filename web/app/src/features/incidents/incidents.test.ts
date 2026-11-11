// SPDX-License-Identifier: Apache-2.0

import type { Incident } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import { describeEvent, formatDuration, incidentStatus } from "./model";

const incident: Incident = {
  id: "inc_1",
  projectId: "prj_1",
  monitorName: "Orders",
  state: "open",
  openedAt: "2026-11-04T12:00:00Z",
  firstCheck: { at: "2026-11-04T12:00:00Z" },
};

describe("incidents", () => {
  it("say where they stand", () => {
    expect(incidentStatus(incident)).toEqual({ label: "Open", mark: "down" });
    expect(incidentStatus({ ...incident, acknowledgedAt: "2026-11-04T12:01:00Z" }).label).toBe("Acknowledged");
    expect(incidentStatus({ ...incident, state: "resolved", acknowledgedAt: "2026-11-04T12:01:00Z" }).label).toBe("Resolved");
  });

  it("say how long they lasted", () => {
    const from = "2026-11-04T12:00:00Z";
    expect(formatDuration(from, "2026-11-04T12:00:45Z")).toBe("45 s");
    expect(formatDuration(from, "2026-11-04T12:12:30Z")).toBe("12 min");
    expect(formatDuration(from, "2026-11-04T15:05:00Z")).toBe("3 h 5 min");
    expect(formatDuration(from, "2026-11-04T14:00:00Z")).toBe("2 h");
    expect(formatDuration(from, "2026-11-06T16:00:00Z")).toBe("2 d 4 h");
    expect(formatDuration(from, undefined, Date.parse("2026-11-04T12:02:00Z"))).toBe("2 min");
  });

  it("put their timeline in words", () => {
    expect(describeEvent({ at: "", kind: "state", from: "down", to: "degraded" })).toBe("The monitor went from down to degraded.");
    expect(describeEvent({ at: "", kind: "resolved", detail: "Resolved by hand." })).toBe("Resolved by hand.");
    expect(describeEvent({ at: "", kind: "opened", to: "down" })).toBe("Opened: the monitor went down.");
  });
});
