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

  // The project's API client sends a request from the server and keeps it
  // in history. Inside the Compose network, the server is signalbox:8080.
  await page.goto(`${projectURL}/requests`);
  await expect(page.getByRole("heading", { level: 1, name: "Requests" })).toBeVisible();
  await page.getByLabel("URL").fill("http://signalbox:8080/api/v1/meta");
  await page.getByRole("button", { name: "Send" }).click();
  const response = page.getByRole("region", { name: "Response" });
  await expect(response.getByText("200", { exact: true })).toBeVisible();
  await expect(response.getByText(/"version"/)).toBeVisible();
  await page.getByRole("tab", { name: "History" }).click();
  await expect(page.getByRole("tabpanel", { name: "Your history" }).getByText("http://signalbox:8080/api/v1/meta")).toBeVisible();
  await expectAccessible(page);

  // Saved in a collection, it opens again after a reload.
  await page.getByRole("tab", { name: "Collections" }).click();
  await page.getByRole("button", { name: "New collection" }).click();
  await page.getByLabel("Collection name").fill("Shop API");
  await page.getByRole("button", { name: "Add collection" }).click();
  await page.getByRole("button", { name: "Save as…" }).click();
  const saveForm = page.getByRole("form", { name: "Save request" });
  await saveForm.getByLabel("Name").fill("Meta");
  await saveForm.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Meta" })).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "Open Shop API" }).click();
  await page.getByRole("button", { name: /GET Meta/ }).click();
  await expect(page.getByLabel("URL")).toHaveValue("http://signalbox:8080/api/v1/meta");
  await page.getByLabel("URL").fill("http://signalbox:8080/livez");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  await page.reload();
  await page.getByRole("button", { name: "Open Shop API" }).click();
  await page.getByRole("button", { name: /GET Meta/ }).click();
  await expect(page.getByLabel("URL")).toHaveValue("http://signalbox:8080/livez");
  await expectAccessible(page);
  // Cloud metadata endpoints are never reached.
  await page.getByLabel("URL").fill("http://169.254.169.254/latest/meta-data/");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(response.getByRole("alert")).toContainText("Signalbox doesn't send requests to 169.254.169.254");

  // An environment's variables fill in {{name}}; its secrets stay hidden.
  await page.getByLabel("Environment").selectOption({ label: "Staging" });
  await page.getByRole("button", { name: "Variables" }).click();
  const variables = page.getByRole("form", { name: "Staging variables" });
  await variables.getByLabel("Variable 1 name").fill("baseUrl");
  await variables.getByLabel("Variable 1 value").fill("http://signalbox:8080");
  await variables.getByLabel("Variable 2 name").fill("token");
  await variables.getByLabel("Variable 2 value").fill("e2e-secret-token");
  await variables.getByLabel("Variable 2 is a secret").check();
  await variables.getByRole("button", { name: "Save variables" }).click();
  await expect(page.getByRole("region", { name: "Staging variables" }).getByRole("status")).toHaveText("Saved.");
  await expect(variables.getByLabel("Variable 2 value")).toHaveAttribute("placeholder", "Stored. Type to replace it.");
  await expectAccessible(page);
  await page.getByLabel("URL", { exact: true }).fill("{{baseUrl}}/api/v1/meta?{{missing}}");
  await page.getByLabel("Header 1 name").fill("X-Token");
  await page.getByLabel("Header 1 value").fill("{{token}}");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(response.getByText("200", { exact: true })).toBeVisible();
  await expect(page.getByText("sent as typed: {{missing}}")).toBeVisible();
  // The choice is remembered, and history shows what was sent.
  await page.reload();
  await expect(page.getByLabel("Environment")).toHaveValue(/^env_/);
  await page.getByRole("tab", { name: "History" }).click();
  await expect(page.getByRole("tabpanel", { name: "Your history" }).getByText("http://signalbox:8080/api/v1/meta?{{missing}}")).toBeVisible();

  // A Postman collection imports with a report, and exports again.
  const postman = {
    info: { name: "Imported shop", schema: "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" },
    variable: [{ key: "host", value: "http://signalbox:8080" }],
    item: [
      {
        name: "Health",
        item: [
          { name: "Meta", request: { method: "GET", url: "{{host}}/api/v1/meta" } },
          { name: "Upload", request: { method: "POST", url: "{{host}}/x", body: { mode: "file", file: { src: "a.bin" } } } },
        ],
      },
    ],
  };
  await page.getByRole("tab", { name: "Collections" }).click();
  await page.getByRole("button", { name: "Import from Postman" }).click();
  await page.getByLabel("Postman collection", { exact: true }).setInputFiles({ name: "shop.postman_collection.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(postman)) });
  const importReport = page.getByRole("group", { name: "Import report" });
  await expect(importReport).toContainText("1 folder, 2 requests, 1 variable.");
  await expect(importReport).toContainText("The file body wasn't imported");
  await expectAccessible(page);
  await page.getByRole("button", { name: "Import shop.postman_collection.json" }).click();
  await expect(page.getByText("Imported Imported shop.")).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Open Imported shop" }).click();
  await expect(page.getByRole("link", { name: "Export Imported shop for Postman" })).toHaveAttribute("href", /\/api\/v1\/collections\/col_.+\/export$/);
  await page.getByRole("button", { name: /GET Meta/ }).click();
  await expect(page.getByLabel("URL", { exact: true })).toHaveValue("{{host}}/api/v1/meta");
  // The collection's variables fill in {{host}}.
  await page.getByLabel("Environment").selectOption("");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(response.getByText("200", { exact: true })).toBeVisible();
  // Its test script runs against the response.
  await page.getByRole("tab", { name: "Scripts", exact: true }).click();
  await page.getByLabel("Test script").fill(`pm.test("meta has a version", () => pm.expect(pm.response.json()).to.have.property("version"));`);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(response.getByRole("tab", { name: "Tests (1/1)" })).toBeVisible();
  await expect(response.getByText("meta has a version")).toBeVisible();

  // Saved, its folder runs as a test suite.
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  // A row's actions show when it's hovered or focused.
  await page.getByRole("button", { name: "Health", exact: true }).hover();
  await page.getByRole("link", { name: "Run Health" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Runs of Imported shop" })).toBeVisible();
  await expect(page.getByLabel("What to run")).toHaveValue(/^item_/);
  await expectAccessible(page);
  await page.getByRole("button", { name: "Start run" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Health" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Run status" })).toHaveText("Run status: Passed", { timeout: 30_000 });
  const totals = page.getByLabel("Totals");
  await expect(totals.getByText("Requests", { exact: true }).locator("xpath=following-sibling::dd")).toHaveText("2");
  await expect(totals.getByText("Tests", { exact: true }).locator("xpath=following-sibling::dd")).toHaveText("1");
  await page.getByRole("button", { name: /Meta/ }).click();
  await expect(page.getByText("meta has a version")).toBeVisible();
  await expect(page.getByRole("link", { name: "JUnit" })).toHaveAttribute("href", /\/api\/v1\/runs\/run_.+\/report\?format=junit$/);
  await expectAccessible(page);
  const junit = await page.request.get(await page.getByRole("link", { name: "JUnit" }).getAttribute("href") ?? "");
  expect(await junit.text()).toContain('<testcase name="meta has a version"');
  await page.goto(`${projectURL}/requests`);

  // A curl command fills in the editor.
  await page.getByRole("button", { name: "Import cURL" }).click();
  await page.getByLabel("curl command", { exact: true }).fill("curl 'http://signalbox:8080/livez' -H 'accept: application/json' --insecure");
  await page.getByRole("button", { name: "Read command" }).click();
  await expect(page.getByLabel("URL", { exact: true })).toHaveValue("http://signalbox:8080/livez");
  await expect(page.getByRole("group", { name: "Import report" })).toContainText("curl's --insecure option isn't supported");
  await page.goto(projectURL);

  // Double-clicking opens the service.
  await orders.dblclick();
  await expect(page.getByRole("heading", { level: 1, name: "Orders" })).toBeVisible();
});
