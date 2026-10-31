// SPDX-License-Identifier: Apache-2.0

import { expectAccessible, openAsAdmin, test, expect } from "./support";

// The demo shop runs beside Signalbox when deploy/compose/compose.demo.yaml
// is in the stack, as in CI. Its seed waits for the tests to complete
// first-run setup, then sets the shop up as the admin.
test.skip(!process.env.CI && !process.env.SIGNALBOX_E2E_DEMO, "the demo shop runs in CI, or with SIGNALBOX_E2E_DEMO set");

test("the demo shop sends its traces to Signalbox", async ({ page, request }) => {
  test.setTimeout(180_000);
  await openAsAdmin(page, request);

  // The seed made the workspace, the project and its services.
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("link", { name: "Demo shop" })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 90_000 });
  await page.getByRole("link", { name: "Demo shop" }).click();
  // The link's name includes the project's description.
  await page.getByRole("link", { name: /^Shop\b/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
  const services = page.getByRole("region", { name: "Services" });
  for (const name of ["Storefront", "Orders", "Inventory", "Payments"]) {
    await expect(services.getByText(name, { exact: true })).toBeVisible();
  }

  // The shop's services send with the key the seed made, over gRPC and HTTP.
  await page.getByRole("link", { name: "Ingest keys" }).click();
  await page.getByLabel("Environment").selectOption({ label: "Production" });
  await expect(page.getByText("demo-shop", { exact: false })).toBeVisible();
  await expect(async () => {
    await page.reload();
    await page.getByLabel("Environment").selectOption({ label: "Production" });
    await expect(page.getByText(/last used/)).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
  await expectAccessible(page);

  // The shop itself works, and its health checks answer in health+json.
  const products = await request.get("http://localhost:8090/api/products");
  expect(products.status()).toBe(200);
  const health = await request.get("http://localhost:8091/health");
  expect(health.headers()["content-type"]).toBe("application/health+json");
});

test("a request's trace opens from its response, and traces can be searched", async ({ page, request }) => {
  test.setTimeout(180_000);
  await openAsAdmin(page, request);
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("link", { name: "Demo shop" })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 90_000 });
  await page.getByRole("link", { name: "Demo shop" }).click();
  await page.getByRole("link", { name: /^Shop\b/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
  const projectURL = page.url();

  // Signalbox sends the request with a traceparent, and the shop's
  // services continue the trace.
  await page.goto(`${projectURL}/requests`);
  await page.getByLabel("URL").fill("http://storefront-web:8080/api/products");
  await page.getByRole("button", { name: "Send" }).click();
  const response = page.getByRole("region", { name: "Response" });
  await expect(response.getByText("TEA-01").first()).toBeVisible();
  await response.getByRole("link", { name: "Open trace" }).click();
  await expect(page).toHaveURL(/\/traces\/[0-9a-f]{32}$/);
  // Each service sends its spans every few seconds, in batches of its own.
  const waterfall = page.getByRole("table", { name: /Spans of the trace/ });
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("heading", { level: 1, name: "storefront-web: GET /api/products" })).toBeVisible({ timeout: 2_000 });
    await expect(waterfall.getByRole("button", { name: /inventory-api GET \/products/ })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
  // Its root's parent is the request Signalbox sent, which isn't a span.
  await expect(waterfall.getByText("Parent not received")).toBeVisible();
  await waterfall.getByRole("button", { name: /inventory-api GET \/products/ }).click();
  await expect(page.getByRole("region", { name: "inventory-api: GET /products" }).getByText("http.route")).toBeVisible();
  await expectAccessible(page);

  // Searching finds the shop's checkouts, with their database work.
  await page.getByRole("link", { name: "Search traces" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Traces" })).toBeVisible();
  await page.getByLabel("Service").selectOption("orders-api");
  // Some checkouts find a product out of stock and save nothing; searching
  // by the insert finds those that did.
  await page.getByLabel("Operation").fill("INSERT orders");
  await page.getByRole("button", { name: "Search" }).click();
  const results = page.getByRole("table");
  await expect(results.getByRole("link", { name: /storefront-web: POST \/api\/checkout/ }).first()).toBeVisible();
  await expectAccessible(page);
  await results.getByRole("link", { name: /storefront-web: POST \/api\/checkout/ }).first().click();
  await expect(page.getByRole("table", { name: /Spans of the trace/ }).getByRole("button", { name: /orders-api INSERT orders/ })).toBeVisible();
});

test("the shop's services and their calls", async ({ page, request }) => {
  // Lines are built a minute at a time, about two minutes after the calls.
  test.setTimeout(300_000);
  await openAsAdmin(page, request);
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("link", { name: "Demo shop" })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 90_000 });
  await page.getByRole("link", { name: "Demo shop" }).click();
  await page.getByRole("link", { name: /^Shop\b/ }).click();
  await page.getByRole("link", { name: "Traces", exact: true }).click();
  await page.getByRole("tab", { name: "Services" }).click();

  const services = page.getByRole("region", { name: "Services" });
  await expect(services.getByRole("button", { name: "orders-api" })).toBeVisible();
  await services.getByRole("button", { name: "orders-api" }).click();
  await expect(page.getByRole("table", { name: "Operations of orders-api" }).getByText("POST /orders")).toBeVisible();

  // The orders service calls payments, its database and, through payments,
  // the payment provider outside the shop.
  const calls = page.getByRole("region", { name: "Calls" });
  await expect(async () => {
    await page.reload();
    await expect(calls.getByRole("row", { name: /^orders-api payments-api \d/ })).toBeVisible({ timeout: 2_000 });
    await expect(calls.getByRole("row", { name: /^orders-api shop-db Database, postgresql/ })).toBeVisible({ timeout: 2_000 });
    await expect(calls.getByRole("row", { name: /^payments-api psp\.example External/ })).toBeVisible({ timeout: 2_000 });
    // Calls made while payments' spans were refused, before the seed made
    // its key, still count as calls to payments.
    await expect(calls.getByRole("row", { name: /payments-api External/ })).toHaveCount(0);
  }).toPass({ timeout: 240_000 });
  await expectAccessible(page);
});
