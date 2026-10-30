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
