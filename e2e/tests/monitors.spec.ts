// SPDX-License-Identifier: Apache-2.0

import { expect, expectAccessible, openAsAdmin, runID, test } from "./support";

// A monitor made by hand, of Signalbox itself: inside the Compose network,
// the server is signalbox:8080.

test("a monitor checks a URL, and says when it's wrong", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  await page.getByLabel("Name", { exact: true }).fill(`Watched ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Watched ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/prj_[0-9a-z]+$/);

  await page.getByRole("link", { name: "Monitors" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Monitors" })).toBeVisible();
  await expect(page.getByText("No monitors yet")).toBeVisible();
  await expectAccessible(page);

  // Adding one, with an assertion on the body.
  const form = page.getByRole("form", { name: "New monitor" });
  await form.getByLabel("Name", { exact: true }).fill("Meta");
  await form.getByLabel("URL").fill("http://signalbox:8080/api/v1/meta");
  await form.getByRole("button", { name: "Add assertion" }).click();
  await form.getByLabel("Assertion 1 kind").selectOption({ label: "JSONPath" });
  await form.getByLabel("Assertion 1 JSONPath").fill("$.version");
  await form.getByLabel("Assertion 1 comparison").selectOption({ label: "exists" });
  await expectAccessible(page);
  await form.getByRole("button", { name: "Add monitor" }).click();

  await expect(page.getByRole("heading", { level: 1, name: "Meta" })).toBeVisible();
  await expect(page).toHaveURL(/\/monitors\/mon_[0-9a-z]+$/);
  const state = page.getByRole("status", { name: /^State:/ });
  await expect(state).toHaveAccessibleName("State: Pending");

  // Checked now, it's up, and the check is there to read.
  await page.getByRole("button", { name: "Check now" }).click();
  await expect(state).toHaveAccessibleName("State: Up");
  const checks = page.getByRole("table", { name: "Checks" });
  await expect(checks.getByRole("cell", { name: "Passed" }).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.locator(".check-bar").first()).toBeVisible();
  await expectAccessible(page);

  // An assertion that can't hold fails it; the first failure only starts a
  // confirmation, and one more in a row takes it down.
  const settings = page.getByRole("form", { name: "Monitor settings" });
  await settings.getByLabel("Assertion 1 JSONPath").fill("$.nothing");
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect(settings.getByRole("button", { name: "Save settings" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Check now" })).toBeEnabled();
  await page.getByRole("button", { name: "Check now" }).click();
  await expect(page.getByText("1 of 2 failures in a row")).toBeVisible();
  await expect(state).toHaveAccessibleName("State: Up");
  await page.getByRole("button", { name: "Check now" }).click();
  await expect(state).toHaveAccessibleName("State: Down");
  await expect(page.getByText("The value at $.nothing is missing.").first()).toBeVisible();
  await expectAccessible(page);

  // Paused, it stays paused, and resumes as pending.
  await page.getByRole("button", { name: "Pause" }).click();
  await expect(state).toHaveAccessibleName("State: Paused");
  await page.getByRole("button", { name: "Resume" }).click();
  await expect(state).toHaveAccessibleName("State: Pending");

  // The list shows it, with its uptime.
  await page.getByRole("link", { name: "Monitors" }).click();
  const list = page.getByRole("table", { name: "Monitors" });
  await expect(list.getByRole("link", { name: "Meta" })).toBeVisible();
  await expect(list.getByRole("cell", { name: "Pending" })).toBeVisible();
  await expectAccessible(page);

  // Deleted, it's gone.
  await list.getByRole("link", { name: "Meta" }).click();
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("No monitors yet")).toBeVisible();
});
