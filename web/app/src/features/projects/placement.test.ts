// SPDX-License-Identifier: Apache-2.0

import type { Service } from "@signalbox/api-client";
import { describe, expect, it } from "vitest";
import type { ServiceNode } from "../canvas/model";
import { finishedMoves, servicesToNodes } from "./placement";

const service = (id: string, name: string, x: number, y: number): Service => ({
  id,
  projectId: "prj_1",
  name,
  slug: name.toLowerCase(),
  position: { x, y },
  createdAt: "2026-10-18T12:00:00Z",
  updatedAt: "2026-10-18T12:00:00Z",
});

describe("servicesToNodes", () => {
  it("places services where they were saved", () => {
    const nodes = servicesToNodes([service("svc_a", "A", 10, 20)], [], true);
    expect(nodes).toEqual([
      { id: "svc_a", type: "service", position: { x: 10, y: 20 }, selected: undefined, draggable: true, data: { name: "A", kind: "service", health: "unknown" } },
    ]);
  });

  it("keeps local moves and selection, takes new names, and drops removed services", () => {
    const current: ServiceNode[] = [
      { id: "svc_a", type: "service", position: { x: 99, y: 99 }, selected: true, data: { name: "A", kind: "service", health: "unknown" } },
      { id: "svc_gone", type: "service", position: { x: 0, y: 0 }, data: { name: "Gone", kind: "service", health: "unknown" } },
    ];
    const nodes = servicesToNodes([service("svc_a", "A renamed", 10, 20), service("svc_b", "B", 288, 0)], current, false);
    expect(nodes.map((n) => [n.id, n.data.name, n.position, n.selected, n.draggable])).toEqual([
      ["svc_a", "A renamed", { x: 99, y: 99 }, true, false],
      ["svc_b", "B", { x: 288, y: 0 }, undefined, false],
    ]);
  });
});

describe("finishedMoves", () => {
  it("saves drops and key moves, not moves in progress", () => {
    expect(
      finishedMoves([
        { type: "position", id: "dragging", position: { x: 1, y: 1 }, dragging: true },
        { type: "position", id: "dropped", dragging: false },
        { type: "position", id: "keyed", position: { x: 5, y: 0 } },
        { type: "select", id: "selected", selected: true },
      ]),
    ).toEqual(["dropped", "keyed"]);
  });
});
