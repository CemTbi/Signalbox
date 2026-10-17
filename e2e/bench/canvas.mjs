// SPDX-License-Identifier: Apache-2.0

// Measures the canvas benchmark page (web/app/bench/canvas.html) in
// Chromium: layout and first-render time, the cost of live updates, and
// frame times while updates arrive and while the canvas pans. Spike 3,
// ADR-0018.
//
//   npm --prefix web run dev            # in another terminal
//   node e2e/bench/canvas.mjs [--headed] [--quick]

import { chromium } from "@playwright/test";

const base = process.env.BENCH_URL ?? "http://localhost:5173/bench/canvas.html";
const headed = process.argv.includes("--headed");
const quick = process.argv.includes("--quick");
const only = process.argv.find((a) => a.startsWith("--update="))?.slice(9);
const hold = process.argv.includes("--no-hold") ? "0" : "1";
const every = process.argv.find((a) => a.startsWith("--every="))?.slice(8) ?? "1000";

const q = (xs, p) => {
  const s = [...xs].sort((a, b) => a - b);
  return s.length ? s[Math.min(s.length - 1, Math.floor(p * s.length))] : NaN;
};
const ms = (x) => (Number.isFinite(x) ? `${x.toFixed(0)}`.padStart(5) : "    -");
const pct = (xs, limit) => `${((100 * xs.filter((x) => x > limit).length) / Math.max(1, xs.length)).toFixed(1)}%`.padStart(6);

const configs = [];
for (const nodes of quick ? [200] : [50, 200, 400])
  for (const layout of ["elk", "dagre"])
    for (const update of only ? [only] : nodes === 200 ? ["store", "patch", "all"] : ["store"])
      for (const cpu of process.argv.includes("--cpu2") ? [2] : [1, 2, 4]) configs.push({ nodes, layout, update, cpu });

const browser = await chromium.launch({ headless: !headed });
console.log(`Chromium ${browser.version()}, ${headed ? "headed" : "headless"}, updates held while moving: ${hold === "1" ? "yes" : "no"}; times in ms\n`);
console.log("nodes edges layout update cpu | layout first-render | update p50 p95 | idle frame p95 >33ms | pan frame p95 >33ms");
for (const c of configs) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: c.cpu });
  await page.goto(`${base}?nodes=${c.nodes}&layout=${c.layout}&update=${c.update}&every=${every}&hold=${hold}`);
  await page.waitForFunction(() => window.__bench?.ready, null, { timeout: 120_000 });
  const first = await page.evaluate(() => ({ layoutMs: __bench.layoutMs, renderMs: __bench.renderMs, nodes: __bench.nodes, edges: __bench.edges }));
  const idle = await page.evaluate(() => __bench.frames(6000, false));
  const pan = await page.evaluate(() => __bench.frames(6000, true));
  const updates = await page.evaluate(() => __bench.updates);
  console.log(
    `${String(first.nodes).padStart(5)} ${String(first.edges).padStart(5)} ${c.layout.padEnd(6)} ${c.update.padEnd(6)} ${c.cpu}x  | ${ms(first.layoutMs)} ${ms(first.renderMs)}        | ${ms(q(updates, 0.5))} ${ms(q(updates, 0.95))} | ${ms(q(idle, 0.95))} ${pct(idle, 33.4)}     | ${ms(q(pan, 0.95))} ${pct(pan, 33.4)}`,
  );
  await page.close();
}
await browser.close();
