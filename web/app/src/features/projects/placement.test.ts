// SPDX-License-Identifier: Apache-2.0

import type { Dependency, LiveLine, Service } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import { applyLive } from "../canvas/liveData";
import { type ServiceNode, NODE_HEIGHT, NODE_WIDTH } from "../canvas/model";
import { canvasEdges, chooseEnvironment, liveValues, placeNear, servicesToNodes, targetsOf, targetsToNodes } from "./placement";

const line = (from: string, to: string, over: Partial<LiveLine> = {}): LiveLine => ({
  id: `${from}>${to}`,
  from,
  to,
  fromName: from,
  toName: to.replace(/^[a-z]+:/, ""),
  toKind: to.startsWith("database:") ? "database" : to.startsWith("external:") ? "external" : "service",
  rate: 1,
  errorRate: 0,
  ...over,
});

const node = (id: string, x: number, y: number): ServiceNode => ({ id, type: "service", position: { x, y }, data: { name: id, kind: "service", health: "unknown" } });

describe("targetsOf", () => {
  it("lists databases and hosts called from the canvas", () => {
    const targets = targetsOf(
      [
        line("svc_b", "database:shop-db", { system: "postgresql" }),
        line("svc_a", "database:shop-db"),
        line("svc_a", "external:psp.example"),
        line("svc_a", "svc_b"),
        // Called only by a service that isn't on the canvas.
        line("seen:cron", "external:mail.example"),
      ],
      new Set(["svc_a", "svc_b"]),
    );
    expect(targets.map((t) => t.id)).toEqual(["database:shop-db", "external:psp.example"]);
    expect(targets[0]).toMatchObject({ kind: "database", name: "shop-db", system: "postgresql", callers: ["svc_a", "svc_b"] });
  });
});

describe("placeNear", () => {
  it("goes to the right of its callers, level with them", () => {
    const spot = placeNear([{ x: 0, y: 0 }, { x: 320, y: 192 }], []);
    expect(spot.x).toBeGreaterThanOrEqual(320 + NODE_WIDTH);
    expect(spot.y).toBe(96);
  });

  it("never lands on another node", () => {
    const taken = [{ x: 320, y: 0 }, { x: 320, y: 120 }];
    const spot = placeNear([{ x: 0, y: 0 }], taken);
    for (const t of taken) {
      expect(Math.abs(spot.x - t.x) >= NODE_WIDTH || Math.abs(spot.y - t.y) >= NODE_HEIGHT).toBe(true);
    }
  });
});

describe("targetsToNodes", () => {
  const db = { id: "database:shop-db", kind: "database" as const, name: "shop-db", system: "postgresql", callers: ["svc_a"] };

  it("uses the saved place, and keeps one on the canvas where it is", () => {
    const saved = [{ kind: "database" as const, name: "shop-db", x: 40, y: 80 }];
    expect(targetsToNodes([db], saved, [], [node("svc_a", 0, 0)], true)[0]?.position).toEqual({ x: 40, y: 80 });
    expect(targetsToNodes([db], saved, [node(db.id, 7, 9)], [node("svc_a", 0, 0)], true)[0]?.position).toEqual({ x: 7, y: 9 });
  });

  it("places new ones apart, and never as an end of a dependency", () => {
    const other = { ...db, id: "database:other", name: "other" };
    const nodes = targetsToNodes([db, other], [], [], [node("svc_a", 0, 0)], true);
    expect(nodes[0]?.position).not.toEqual(nodes[1]?.position);
    expect(nodes[0]?.connectable).toBe(false);
    expect(nodes[0]?.data).toMatchObject({ kind: "database", system: "postgresql" });
  });
});

describe("servicesToNodes", () => {
  it("lets editors draw lines", () => {
    const service = { id: "svc_a", name: "A", position: { x: 1, y: 2 } } as Service;
    expect(servicesToNodes([service], [], true)[0]?.connectable).toBe(true);
    expect(servicesToNodes([service], [], false)[0]?.connectable).toBe(false);
  });
});

describe("canvasEdges", () => {
  const ids = new Map([
    ["svc_a", "A"],
    ["svc_b", "B"],
    ["database:db", "db"],
  ]);
  const dep = (from: string, to: string): Dependency => ({ id: `dep_${from}${to}`, fromServiceId: from, toServiceId: to, createdAt: "" });

  it("draws traffic solid and declared lines dashed, and traffic takes a declared line over", () => {
    const edges = canvasEdges([line("svc_a", "svc_b"), line("svc_a", "database:db")], [dep("svc_a", "svc_b"), dep("svc_b", "svc_a")], ids);
    expect(edges.map((e) => [e.id, e.data?.declared])).toEqual([
      ["svc_a>database:db", false],
      ["svc_a>svc_b", false],
      ["svc_b>svc_a", true],
    ]);
    expect(edges.map((e) => e.ariaLabel)).toEqual(["A calls db", "A calls B", "B is declared to call A"]);
  });

  it("leaves out lines to nodes that aren't on the canvas", () => {
    expect(canvasEdges([line("seen:cron", "svc_a"), line("svc_a", "seen:cron")], [], ids)).toEqual([]);
  });
});

describe("liveValues", () => {
  it("gives a database the calls to it, added up", () => {
    const { nodes, edges } = liveValues(
      [{ id: "svc_a", rate: 12, errorRate: 0.01, p95Ms: 40 }],
      [line("svc_a", "database:db", { rate: 3, errorRate: 0.5 }), line("svc_b", "database:db", { rate: 1, errorRate: 0 })],
    );
    expect(nodes.get("svc_a")).toEqual({ health: "unknown", rps: 12, errorRate: 0.01, p95Ms: 40 });
    expect(nodes.get("database:db")).toEqual({ health: "unknown", rps: 4, errorRate: 0.375, p95Ms: undefined });
    expect(edges.get("svc_a>database:db")).toEqual({ rps: 3, errorRate: 0.5 });
  });
});

describe("applyLive", () => {
  const a = { id: "a", name: "a", onCanvas: true, rate: 1, errorRate: 0 };
  const b = { id: "b", name: "b", onCanvas: true, rate: 2, errorRate: 0 };
  const removed = { services: [], lines: [] };

  it("takes everything when the answer holds everything", () => {
    const got = applyLive({ tag: "old", services: [a], lines: [] }, { services: [b], lines: [], removed }, "new");
    expect(got).toEqual({ tag: "new", services: [b], lines: [] });
  });

  it("applies changes to the state they're for", () => {
    const l = line("a", "b");
    const got = applyLive(
      { tag: "old", services: [a, b], lines: [l] },
      { changesSince: "old", services: [{ ...a, rate: 9 }], lines: [], removed: { services: ["b"], lines: [l.id] } },
      "new",
    );
    expect(got).toEqual({ tag: "new", services: [{ ...a, rate: 9 }], lines: [] });
  });

  it("asks for everything next, if changes are for another state", () => {
    const got = applyLive({ tag: "mine", services: [a], lines: [] }, { changesSince: "other", services: [b], lines: [], removed }, "new");
    expect(got).toEqual({ tag: "", services: [a], lines: [] });
  });
});

describe("chooseEnvironment", () => {
  const envs = [
    { id: "dev", kind: "development" },
    { id: "prod", kind: "production" },
  ];

  it("keeps the choice while it exists, and otherwise shows production", () => {
    expect(chooseEnvironment(envs, "dev")).toBe("dev");
    expect(chooseEnvironment(envs, "gone")).toBe("prod");
    expect(chooseEnvironment([{ id: "dev", kind: "development" }], "")).toBe("dev");
    expect(chooseEnvironment([], "")).toBe("");
  });
});
