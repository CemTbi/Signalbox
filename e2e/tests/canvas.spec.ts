// SPDX-License-Identifier: Apache-2.0

import type { Locator, Page } from "@playwright/test";
import { expect, expectAccessible, openAsAdmin, runID, test } from "./support";

// Services placed on a project's canvas by hand: dragged, moved with the
// keyboard, or arranged automatically, and still there after a reload.

/** A node's place on the canvas, from React Flow's transform. */
async function place(node: Locator) {
  const transform = await node.evaluate((el) => (el as HTMLElement).style.transform);
  const [, x, y] = /translate\(([-\d.]+)px,\s*([-\d.]+)px\)/.exec(transform) ?? [];
  return { x: Number(x), y: Number(y) };
}

function node(page: Page, name: string) {
  return page.locator(".react-flow__node").filter({ hasText: name });
}

/** Waits for a move to save: the status says Saving… from the move until the save finishes. */
async function saved(page: Page) {
  const status = page.locator(".canvas-status");
  await expect(status).toHaveText("Saving…");
  await expect(status).toHaveText("Layout saved");
}

test("services are placed on the canvas by hand and stay where they are put", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  const workspace = `Canvas ${run}`;
  await page.getByLabel("Name", { exact: true }).fill(workspace);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: workspace })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
  const projectURL = page.url();
  await expect(page.getByText("No services on this canvas yet")).toBeVisible();

  // Two services take the first two spots of the grid.
  for (const name of ["Orders", "Payments"]) {
    await page.goto(projectURL);
    await page.getByRole("region", { name: "Add a service" }).getByLabel("Name", { exact: true }).fill(name);
    await page.getByRole("button", { name: "Add service" }).click();
    await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  }
  await page.goto(projectURL);
  const orders = node(page, "Orders");
  const payments = node(page, "Payments");
  await expect(orders).toBeVisible();
  await expect(payments).toBeVisible();
  expect(await place(orders)).toEqual({ x: 0, y: 0 });
  expect(await place(payments)).toEqual({ x: 288, y: 0 });
  await expectAccessible(page);

  // Dragged, it stays where it was dropped.
  const box = await orders.boundingBox();
  if (!box) throw new Error("Orders has no box");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 40, box.y + box.height / 2 + 160, { steps: 8 });
  await page.mouse.up();
  await saved(page);
  const dropped = await place(orders);
  expect(dropped.y).toBeGreaterThan(50);

  // Moved with the arrow keys.
  await payments.click();
  for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
  await saved(page);
  const keyed = await place(payments);
  expect(keyed.x).toBeGreaterThan(288);

  await page.reload();
  await expect(orders).toBeVisible();
  expect(await place(orders)).toEqual(dropped);
  expect(await place(payments)).toEqual(keyed);

  // Arranged automatically, and still arranged after a reload.
  await page.getByRole("button", { name: "Arrange automatically" }).click();
  await saved(page);
  const arranged = await place(payments);
  await page.reload();
  await expect(payments).toBeVisible();
  expect(await place(payments)).toEqual(arranged);

  // Double-clicking opens the service.
  await orders.dblclick();
  await expect(page.getByRole("heading", { level: 1, name: "Orders" })).toBeVisible();
});
