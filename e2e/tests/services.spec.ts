// SPDX-License-Identifier: Apache-2.0

import { expect, expectAccessible, openAsAdmin, runID, test } from "./support";

// A service and its OpenAPI spec: add the service to a project, add the
// spec, see its problems, fix one, save a new revision, publish it, read its
// docs and download a client.

const ordersSpec = [
  "openapi: 3.1.1",
  "info:",
  "  title: Orders API",
  "  version: 1.0.0",
  "paths:",
  "  /orders/{id}:",
  "    get:",
  "      parameters:",
  "        - {name: id, in: path, required: true, schema: {type: string}}",
  "      responses:",
  "        '200':",
  "          description: The order.",
  "",
].join("\n");

test("a service's spec is checked, edited, kept in revisions and published", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  await page.getByLabel("Name", { exact: true }).fill(`Orders ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  // The workspace's page replaces the form; filling in earlier types into the old one.
  await expect(page.getByRole("heading", { level: 1, name: `Orders ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();

  // The service.
  await expect(page.getByText("No services yet")).toBeVisible();
  await page.getByRole("region", { name: "Add a service" }).getByLabel("Name", { exact: true }).fill("Orders API");
  await expect(page.getByLabel("Slug")).toHaveValue("orders-api");
  await page.getByRole("button", { name: "Add service" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Orders API" })).toBeVisible();
  await expect(page.getByText("No spec yet")).toBeVisible();
  await expectAccessible(page);

  // Its spec, pasted. The document has no operationId, which the
  // recommended rules warn about.
  await page.getByRole("region", { name: "Add a spec" }).getByLabel("Name", { exact: true }).fill("Orders API");
  await page.getByLabel("Document (YAML or JSON)").fill(ordersSpec);
  await page.getByRole("button", { name: "Add spec" }).click();
  await expect(page.getByText("Orders API · version 1.0.0 · revision 1")).toBeVisible();
  const problems = page.getByRole("region", { name: "Problems in the saved version" });
  await expect(problems.getByText(/operationId/).first()).toBeVisible();
  await expectAccessible(page);

  // Fix it and save revision 2.
  const editor = page.getByLabel("Document (YAML or JSON)");
  await editor.fill(ordersSpec.replace("    get:\n", "    get:\n      operationId: getOrder\n").replace("version: 1.0.0", "version: 1.0.1"));
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await page.getByLabel("What changed").fill("Name the operation");
  await page.getByRole("button", { name: "Save revision" }).click();
  await expect(page.getByText("Orders API · version 1.0.1 · revision 2")).toBeVisible();
  await expect(page.getByText("Unsaved changes")).toHaveCount(0);

  // The history keeps both.
  const history = page.getByRole("region", { name: "History" });
  await expect(history.getByText("Revision 2 (current) · Name the operation")).toBeVisible();
  await expect(history.getByText("Revision 1", { exact: true })).toBeVisible();

  // Publish it. The version comes from the document.
  const versions = page.getByRole("region", { name: "Versions" });
  await expect(versions.getByText("Nothing published yet")).toBeVisible();
  await expect(page.getByLabel("Version", { exact: true })).toHaveValue("1.0.1");
  await page.getByLabel("Release notes (optional)").fill("First release");
  await page.getByRole("button", { name: "Publish 1.0.1" }).click();
  await expect(versions.getByText(/1\.0\.1 · revision 2/)).toBeVisible();
  await expect(page.getByText(/Revision 2 is published as/)).toBeVisible();
  await expectAccessible(page);

  // A frontend calls the mock, without signing in, from another origin.
  const mock = await page.getByLabel("Mock of the latest version").inputValue();
  const answer = await request.get(`${mock}/orders/42`, { headers: { Origin: "http://localhost:3000" } });
  expect(answer.status()).toBe(200);
  expect(answer.headers()["access-control-allow-origin"]).toBe("*");
  const missing = await request.get(`${mock}/refunds`);
  expect(missing.status()).toBe(404);
  expect(((await missing.json()) as { code: string }).code).toBe("no-such-path");

  // The docs show the published version, and try it against the mock.
  await versions.getByRole("link", { name: "Read the docs" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Orders API" })).toBeVisible();
  await expect(page.getByLabel("Version", { exact: true })).toHaveValue("1.0.1");
  const operation = page.getByRole("region", { name: "GET /orders/{id}" });
  await expect(operation.getByText("The order.").first()).toBeVisible();
  await operation.getByRole("button", { name: "Try it against the mock" }).click();
  await operation.getByLabel(/^id/).fill("42");
  await operation.getByRole("button", { name: "Send" }).click();
  await expect(operation.getByText(/ms$/)).toContainText("200");
  await expectAccessible(page);
  await page.goBack();
  await expect(page.getByText(/Revision 2 is published as/)).toBeVisible();

  // A TypeScript client for the published version.
  const code = page.getByRole("region", { name: "Generate code" });
  await expect(code.getByLabel("Generate")).toHaveValue("typescript-client");
  const download = page.waitForEvent("download");
  await code.getByRole("button", { name: "Download zip" }).click();
  expect((await download).suggestedFilename()).toBe("orders-typescript-client-1.0.1.zip");
  await expect(code.getByRole("status")).toContainText("Downloaded orders-typescript-client-1.0.1.zip.");

  // Contract tests for the published version, generated again in place.
  const contracts = page.getByRole("region", { name: "Contract tests" });
  await contracts.getByRole("button", { name: "Generate contract tests" }).click();
  await expect(contracts.getByRole("status")).toContainText(/Made Orders API contract tests with \d+ requests?\./);
  await contracts.getByRole("button", { name: "Generate contract tests" }).click();
  await expect(contracts.getByRole("status")).toContainText("Updated Orders API contract tests");
  await expectAccessible(page);
  await contracts.getByRole("link", { name: "Run them" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Runs of Orders API contract tests" })).toBeVisible();
  await page.goBack();

  // The service page lists the spec.
  await page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: "Orders API" }).click();
  await expect(page.getByRole("link", { name: /Orders API/ })).toBeVisible();
});
