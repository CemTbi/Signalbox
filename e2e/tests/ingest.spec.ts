// SPDX-License-Identifier: Apache-2.0

import { expectAccessible, openAsAdmin, runID, test, expect } from "./support";

// The ingest role's OTLP/HTTP listener, which the Compose stack publishes.
const otlp = process.env.SIGNALBOX_E2E_OTLP_URL ?? "http://localhost:4318";

test("a service sends spans with an ingest key", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);

  await page.getByLabel("Name", { exact: true }).fill(`Traced ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Traced ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/prj_[0-9a-z]+$/);

  // A key for production, shown once with the exporter settings.
  await page.getByRole("link", { name: "Ingest keys" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Ingest keys" })).toBeVisible();
  await page.getByLabel("Environment").selectOption({ label: "Production" });
  await expect(page.getByText("No keys yet")).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("checkout-service");
  await page.getByRole("button", { name: "Create key" }).click();
  const key = await page.getByLabel("Key", { exact: true }).inputValue();
  expect(key).toMatch(/^sbx_ing_/);
  await expect(page.getByLabel("OpenTelemetry settings")).toHaveValue(/OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20sbx_ing_/);
  await expectAccessible(page);
  await page.getByRole("button", { name: "Done" }).click();

  // The service sends a span over OTLP/HTTP, as an OpenTelemetry SDK does.
  const now = BigInt(Date.now()) * 1_000_000n;
  const sent = await request.post(`${otlp}/v1/traces`, {
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    data: {
      resourceSpans: [
        {
          resource: { attributes: [{ key: "service.name", value: { stringValue: "checkout" } }] },
          scopeSpans: [
            {
              spans: [
                {
                  traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
                  spanId: "00f067aa0ba902b7",
                  name: "POST /checkout",
                  kind: 2,
                  startTimeUnixNano: (now - 5_000_000n).toString(),
                  endTimeUnixNano: now.toString(),
                },
              ],
            },
          ],
        },
      ],
    },
  });
  expect(sent.status()).toBe(200);
  expect(await sent.json()).toEqual({});

  // A wrong key is refused, as OTLP says: 401 with a status.
  const refused = await request.post(`${otlp}/v1/traces`, {
    headers: { Authorization: "Bearer sbx_ing_wrong", "Content-Type": "application/json" },
    data: { resourceSpans: [] },
  });
  expect(refused.status()).toBe(401);

  // The key shows it was used.
  await page.reload();
  await page.getByLabel("Environment").selectOption({ label: "Production" });
  await expect(page.getByText(/last used/)).toBeVisible();

  // Revoking it stops it.
  await page.getByRole("button", { name: "Revoke checkout-service" }).click();
  await expect(page.getByText("No keys yet")).toBeVisible();
  const afterRevoke = await request.post(`${otlp}/v1/traces`, {
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    data: { resourceSpans: [] },
  });
  expect(afterRevoke.status()).toBe(401);
});
