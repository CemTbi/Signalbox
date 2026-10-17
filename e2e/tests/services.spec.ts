// SPDX-License-Identifier: Apache-2.0

import { expect, expectAccessible, openAsAdmin, runID, test } from "./support";

// A service and its OpenAPI spec: add the service to a project, add the
// spec, see its problems, fix one and save a new revision.

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

test("a service's spec is checked, edited and kept in revisions", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  await page.getByLabel("Name", { exact: true }).fill(`Orders ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
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

  // The service page lists the spec.
  await page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: "Orders API" }).click();
  await expect(page.getByRole("link", { name: /Orders API/ })).toBeVisible();
});
