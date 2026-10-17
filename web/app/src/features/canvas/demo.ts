// SPDX-License-Identifier: Apache-2.0

import type { CallEdge, Health, ServiceData, ServiceKind, ServiceNode } from "./model";

// A demo shop of any size, for the canvas benchmark and, later, the demo
// installation. Services form layers, as real systems do: frontends call
// gateways, gateways call domain services, services call each other and
// their databases and external providers. The same seed gives the same
// shop.

/** A small, fast, seedable random number generator (mulberry32). */
export function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const domains = ["orders", "payments", "inventory", "catalog", "pricing", "shipping", "users", "search", "reviews", "cart", "loyalty", "tax", "fraud", "notify", "media", "returns"];

export function demoShop(count: number, seed = 1): { nodes: ServiceNode[]; edges: CallEdge[] } {
  const random = rng(seed);
  const pick = <T>(items: readonly T[]) => items[Math.floor(random() * items.length)] as T;
  const nodes: ServiceNode[] = [];
  const edges: CallEdge[] = [];
  const layers: string[][] = [[], [], [], []]; // frontends, gateways, services, stores and externals

  const add = (layer: number, name: string, kind: ServiceKind) => {
    const id = `n${nodes.length}`;
    const data: ServiceData = { name, kind, health: "ok" };
    if (kind === "service" || kind === "mock") data.specVersion = `v${1 + Math.floor(random() * 3)}.${Math.floor(random() * 10)}.${Math.floor(random() * 10)}`;
    nodes.push({ id, type: "service", position: { x: 0, y: 0 }, data });
    layers[layer]?.push(id);
  };

  const frontends = Math.max(1, Math.round(count * 0.05));
  const gateways = Math.max(1, Math.round(count * 0.05));
  const stores = Math.max(1, Math.round(count * 0.3));
  const services = Math.max(1, count - frontends - gateways - stores);
  for (let i = 0; i < frontends; i++) add(0, `${pick(["storefront", "admin", "mobile", "partner"])}-web-${i}`, "service");
  for (let i = 0; i < gateways; i++) add(1, `gateway-${i}`, "service");
  for (let i = 0; i < services; i++) add(2, `${domains[i % domains.length]}-api${i >= domains.length ? `-${Math.floor(i / domains.length)}` : ""}`, random() < 0.08 ? "mock" : "service");
  for (let i = 0; i < stores; i++) {
    const external = random() < 0.25;
    add(3, external ? `${pick(["psp", "sms", "maps", "tax", "mail"])}-${i}.example` : `${domains[i % domains.length]}-db-${i}`, external ? "external" : "database");
  }

  const link = (source: string, target: string) => {
    if (source === target || edges.some((e) => e.source === source && e.target === target)) return;
    edges.push({ id: `${source}-${target}`, source, target, data: { declared: random() < 0.05 } });
  };
  const [fe = [], gw = [], svc = [], st = []] = layers;
  for (const f of fe) for (let k = 0; k < 2; k++) link(f, pick(gw));
  for (const g of gw) for (let k = 0; k < 4; k++) link(g, pick(svc));
  for (const s of svc) {
    if (random() < 0.6) link(s, pick(svc));
    link(s, pick(st));
    if (random() < 0.3) link(s, pick(st));
  }
  // Every store has a caller.
  for (const s of st) if (!edges.some((e) => e.target === s)) link(pick(svc), s);
  return { nodes, edges };
}

/**
 * New metrics for every node and line, as a live canvas receives them every
 * few seconds. Health changes rarely, as it does in practice.
 */
export function nextMetrics(nodes: ServiceNode[], edges: CallEdge[], random: () => number) {
  const health = (h: Health): Health => {
    const r = random();
    if (h === "ok") return r < 0.01 ? "warn" : "ok";
    if (h === "warn") return r < 0.2 ? "ok" : r < 0.25 ? "down" : "warn";
    if (h === "down") return r < 0.3 ? "warn" : "down";
    return "ok";
  };
  const nodeData = new Map<string, Partial<ServiceData>>();
  for (const n of nodes) {
    if (n.data.kind === "mock") continue;
    const h = health(n.data.health);
    const base = n.data.kind === "database" ? 6 : n.data.kind === "external" ? 300 : 80;
    nodeData.set(n.id, {
      health: h,
      p95Ms: Math.round(base * (0.7 + random() * 0.6) * (h === "warn" ? 4 : 1)),
      errorRate: h === "down" ? 0.3 + random() * 0.5 : h === "warn" ? 0.02 + random() * 0.05 : random() * 0.004,
      rps: Math.round(5 + random() * 400),
    });
  }
  const edgeData = new Map<string, number>();
  for (const e of edges) if (!e.data?.declared) edgeData.set(e.id, Math.round(1 + random() * 200));
  return { nodeData, edgeData };
}
