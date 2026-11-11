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

test("a collection monitor runs a collection, and a heartbeat waits for its job", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  await page.getByLabel("Name", { exact: true }).fill(`Runs ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Runs ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/prj_[0-9a-z]+$/);
  const projectURL = new URL(page.url()).pathname;

  // A collection whose Health folder passes, and whose last request fails its test.
  const test200 = { listen: "test", script: { exec: ['pm.test("is up", () => pm.response.to.have.status(200));'] } };
  const postman = {
    info: { name: "Smoke", schema: "https://schema.getpostman.com/json/collection/v2.1.0/collection.json" },
    variable: [{ key: "host", value: "http://signalbox:8080" }],
    item: [
      { name: "Health", item: [{ name: "Meta", request: { method: "GET", url: "{{host}}/api/v1/meta" }, event: [test200] }] },
      { name: "Missing", request: { method: "GET", url: "{{host}}/api/v1/nothing-here" }, event: [test200] },
    ],
  };
  await page.goto(`${projectURL}/requests`);
  await page.getByRole("tab", { name: "Collections" }).click();
  await page.getByRole("button", { name: "Import from Postman" }).click();
  await page.getByLabel("Postman collection", { exact: true }).setInputFiles({ name: "smoke.postman_collection.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(postman)) });
  await page.getByRole("button", { name: "Import smoke.postman_collection.json" }).click();
  await expect(page.getByText("Imported Smoke.")).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();

  // A monitor of the Health folder.
  await page.goto(`${projectURL}/monitors`);
  const form = page.getByRole("form", { name: "New monitor" });
  await form.getByLabel("Kind", { exact: true }).selectOption({ label: "Collection run" });
  await form.getByLabel("Name", { exact: true }).fill("Smoke");
  await form.getByLabel("Collection").selectOption({ label: "Smoke" });
  await form.getByLabel("Run", { exact: true }).selectOption({ label: "Health" });
  await expect(form.getByLabel("URL")).toHaveCount(0);
  await expectAccessible(page);
  await form.getByRole("button", { name: "Add monitor" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Smoke" })).toBeVisible();
  await expect(page).toHaveURL(/\/monitors\/mon_[0-9a-z]+$/);
  const monitorURL = new URL(page.url()).pathname;
  const state = page.getByRole("status", { name: /^State:/ });

  await page.getByRole("button", { name: "Check now" }).click();
  await expect(state).toHaveAccessibleName("State: Up");
  const checks = page.getByRole("table", { name: "Checks" });
  await expect(checks.getByRole("cell", { name: "Passed" }).first()).toBeVisible({ timeout: 15_000 });
  await expectAccessible(page);
  // Its run is kept, and knows whose it is.
  await checks.getByRole("row").nth(1).getByRole("link", { name: "Open" }).last().click();
  await expect(page).toHaveURL(/\/runs\/run_[0-9a-z]+$/);
  await expect(page.getByRole("link", { name: "a monitor" })).toBeVisible();
  await expectAccessible(page);

  // The whole collection fails its test, confirmed by a second run.
  await page.goto(monitorURL);
  const settings = page.getByRole("form", { name: "Monitor settings" });
  await settings.getByLabel("Run", { exact: true }).selectOption({ label: "The whole collection" });
  await settings.getByRole("button", { name: "Save settings" }).click();
  await expect(settings.getByRole("button", { name: "Save settings" })).toBeEnabled();
  for (const want of ["State: Up", "State: Down"]) {
    await expect(page.getByRole("button", { name: "Check now" })).toBeEnabled();
    await page.getByRole("button", { name: "Check now" }).click();
    await expect(state).toHaveAccessibleName(want);
  }
  await expect(page.locator(".monitor-failure:not(.monitor-incident)")).toContainText('1 of 2 tests failed, first "is up" in Missing');
  await expect(page.getByRole("link", { name: "Open the run" })).toBeVisible();
  await expectAccessible(page);

  // A heartbeat: its URL is shown once, and its job's call brings it up.
  await page.goto(`${projectURL}/monitors`);
  await form.getByLabel("Kind", { exact: true }).selectOption({ label: "Heartbeat" });
  await form.getByLabel("Name", { exact: true }).fill("Nightly backup");
  await expect(form.getByLabel("Expect a call every (minutes)")).toHaveValue("1440");
  await form.getByRole("button", { name: "Add monitor" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Nightly backup" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Check now" })).toHaveCount(0);
  const heartbeatURL = page.getByRole("textbox", { name: "Heartbeat URL" });
  await expect(heartbeatURL).toHaveValue(/\/api\/v1\/heartbeats\/sbx_hb_[A-Za-z0-9_-]{43}$/);
  const first = await heartbeatURL.inputValue();
  await expectAccessible(page);
  expect((await page.request.post(first)).status()).toBe(204);
  await page.reload();
  await expect(state).toHaveAccessibleName("State: Up");
  await expect(page.getByText("Its token was shown once")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Heartbeat URL" })).toHaveCount(0);

  // Rotated, the old URL stops working.
  await page.getByRole("button", { name: "Rotate token" }).click();
  await page.getByRole("group", { name: "Rotate the token" }).getByRole("button", { name: "Rotate", exact: true }).click();
  await expect(heartbeatURL).not.toHaveValue(first);
  const second = await heartbeatURL.inputValue();
  expect((await page.request.post(first)).status()).toBe(404);
  expect((await page.request.get(second)).status()).toBe(204);
  await expectAccessible(page);
});

test("a monitor going down opens an incident, which people acknowledge and resolve", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  await page.getByLabel("Name", { exact: true }).fill(`Incidents ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Incidents ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/prj_[0-9a-z]+$/);
  await page.getByRole("link", { name: "Incidents", exact: true }).click();
  await expect(page.getByText("No incidents")).toBeVisible();
  await expectAccessible(page);

  // A monitor of a page that isn't there: 404 isn't 2xx.
  await page.getByRole("link", { name: "monitors" }).click();
  const form = page.getByRole("form", { name: "New monitor" });
  await form.getByLabel("Name", { exact: true }).fill("Missing");
  await form.getByLabel("URL").fill("http://signalbox:8080/api/v1/nothing-here");
  await form.getByRole("button", { name: "Add monitor" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Missing" })).toBeVisible();
  const state = page.getByRole("status", { name: /^State:/ });
  for (const want of ["State: Pending", "State: Down"]) {
    await expect(page.getByRole("button", { name: "Check now" })).toBeEnabled();
    await page.getByRole("button", { name: "Check now" }).click();
    if (want === "State: Pending") await expect(page.getByText("1 of 2 failures in a row")).toBeVisible();
    else await expect(state).toHaveAccessibleName(want);
  }
  await page.getByRole("link", { name: "Open the incident" }).click();
  await expect(page).toHaveURL(/\/incidents\/inc_[0-9a-z]+$/);
  const status = page.getByRole("status", { name: /^Incident:/ });
  await expect(status).toHaveAccessibleName("Incident: Open");
  await expect(page.locator(".monitor-failure:not(.monitor-incident)")).toContainText("404");
  await expect(page.getByText("Opened: the monitor went down.")).toBeVisible();
  await expectAccessible(page);

  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(status).toHaveAccessibleName("Incident: Acknowledged");
  await expect(page.getByRole("listitem").filter({ hasText: "Acknowledged." })).toContainText("Ada Lovelace");
  await page.getByRole("button", { name: "Resolve" }).click();
  await expect(status).toHaveAccessibleName("Incident: Resolved");
  await expect(page.getByText("Resolved by hand.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Resolve" })).toHaveCount(0);
  await expectAccessible(page);

  // The project's incidents list it.
  await page.getByRole("link", { name: "Incidents", exact: true }).click();
  const list = page.getByRole("table", { name: "Incidents" });
  await expect(list.getByRole("link", { name: "Missing" })).toBeVisible();
  await expect(list.getByRole("cell", { name: "Resolved" })).toBeVisible();
  await expectAccessible(page);
});
