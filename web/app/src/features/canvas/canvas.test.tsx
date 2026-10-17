// SPDX-License-Identifier: Apache-2.0

import { Position, ReactFlowProvider } from "@xyflow/react";
import { CallEdge } from "./CallEdge";
import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { demoShop, nextMetrics, rng } from "./demo";
import { LiveMetricsContext } from "./LiveContext";
import { LiveMetrics } from "./live";
import { formatMs, formatRate, ServiceNode } from "./ServiceNode";

describe("demoShop", () => {
  it("builds the same layered shop from the same seed", () => {
    const a = demoShop(200, 3);
    const b = demoShop(200, 3);
    expect(a.nodes).toHaveLength(200);
    expect(a.nodes.map((n) => n.data.name)).toEqual(b.nodes.map((n) => n.data.name));
    expect(a.edges.map((e) => e.id)).toEqual(b.edges.map((e) => e.id));
    // Every database and external host has a caller, and no line loops.
    for (const n of a.nodes.filter((n) => n.data.kind === "database" || n.data.kind === "external")) {
      expect(a.edges.some((e) => e.target === n.id), n.data.name).toBe(true);
    }
    expect(a.edges.every((e) => e.source !== e.target)).toBe(true);
  });

  it("gives every node but mocks new metrics", () => {
    const { nodes, edges } = demoShop(50);
    const { nodeData, edgeData } = nextMetrics(nodes, edges, rng(1));
    const mocks = nodes.filter((n) => n.data.kind === "mock").length;
    expect(nodeData.size).toBe(nodes.length - mocks);
    expect(edgeData.size).toBe(edges.filter((e) => !e.data?.declared).length);
  });
});

describe("LiveMetrics", () => {
  const ok = { health: "ok" as const, p95Ms: 80, errorRate: 0, rps: 10 };

  it("notifies only what changed", () => {
    const live = new LiveMetrics();
    const a = vi.fn();
    const b = vi.fn();
    live.subscribe("n:a", a);
    live.subscribe("n:b", b);
    live.update(new Map([["a", ok], ["b", ok]]), new Map());
    live.update(new Map([["a", { ...ok, rps: 11 }], ["b", ok]]), new Map());
    expect(a).toHaveBeenCalledTimes(2);
    expect(b).toHaveBeenCalledTimes(1);
    expect(live.node("a")?.rps).toBe(11);
  });

  it("holds changes while the canvas moves and shows them after", () => {
    const live = new LiveMetrics();
    const a = vi.fn();
    live.subscribe("e:a-b", a);
    live.hold(true);
    live.update(new Map(), new Map([["a-b", 5]]));
    live.update(new Map(), new Map([["a-b", 6]]));
    expect(a).not.toHaveBeenCalled();
    live.hold(false);
    expect(a).toHaveBeenCalledTimes(1);
    expect(live.edge("a-b")?.rps).toBe(6);
  });

  it("stops notifying after unsubscribe", () => {
    const live = new LiveMetrics();
    const a = vi.fn();
    const off = live.subscribe("n:a", a);
    off();
    live.update(new Map([["a", ok]]), new Map());
    expect(a).not.toHaveBeenCalled();
  });
});

describe("CallEdge", () => {
  const edgeProps = {
    id: "a-b",
    source: "a",
    target: "b",
    sourceX: 0,
    sourceY: 0,
    targetX: 200,
    targetY: 0,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    data: { rps: 42 },
  };

  it("drops the rate when live metrics say there is no traffic", () => {
    const live = new LiveMetrics();
    const { container } = render(
      <ReactFlowProvider>
        <LiveMetricsContext.Provider value={live}>
          <svg>
            <CallEdge {...edgeProps} />
          </svg>
        </LiveMetricsContext.Provider>
      </ReactFlowProvider>,
    );
    // Before live metrics, the line shows the rate it came with.
    expect(container.querySelector(".call-label text")?.textContent).toBe("42/s");
    act(() => live.update(new Map(), new Map([["a-b", 7]])));
    expect(container.querySelector(".call-label text")?.textContent).toBe("7/s");
    act(() => live.update(new Map(), new Map([["a-b", undefined]])));
    expect(container.querySelector(".call-label")).toBeNull();
  });
});

describe("ServiceNode", () => {
  const props = {
    id: "n1",
    type: "service" as const,
    selected: false,
    dragging: false,
    zIndex: 0,
    isConnectable: false,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
    deletable: false,
    draggable: true,
    selectable: true,
    data: { name: "orders-api", kind: "service" as const, specVersion: "v2.3.0", health: "unknown" as const },
  };

  it("shows health in words and live metrics as they arrive", () => {
    const live = new LiveMetrics();
    render(
      <ReactFlowProvider>
        <LiveMetricsContext.Provider value={live}>
          <ServiceNode {...props} />
        </LiveMetricsContext.Provider>
      </ReactFlowProvider>,
    );
    expect(screen.getByText("orders-api")).toBeInTheDocument();
    expect(screen.getByText("Service · v2.3.0")).toBeInTheDocument();
    expect(screen.getByText("No checks")).toBeInTheDocument();
    expect(screen.getByText("No traffic yet")).toBeInTheDocument();

    act(() => live.update(new Map([["n1", { health: "warn", p95Ms: 640, errorRate: 0.024, rps: 12 }]]), new Map()));
    expect(screen.getByText("Degraded")).toBeInTheDocument();
    expect(screen.getByText("p95 640 ms · 2.4% err · 12/s")).toBeInTheDocument();
  });

  it("formats durations and rates", () => {
    expect(formatMs(84)).toBe("84 ms");
    expect(formatMs(1500)).toBe("1.5 s");
    expect(formatRate(0)).toBe("0%");
    expect(formatRate(0.0004)).toBe("<0.1%");
    expect(formatRate(0.024)).toBe("2.4%");
  });
});
