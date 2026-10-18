// SPDX-License-Identifier: Apache-2.0

import { admin, expect, expectAccessible, openAsAdmin, runID, setupRequired, signIn, test } from "./support";

// The walking skeleton's promise: from a new installation, someone can set
// it up, sign in, create a workspace and a project, and see the project's
// canvas. Every page on the way must pass an automated accessibility check
// against WCAG 2.2 AA (ADR-0006).

test("from a new installation to a project's canvas", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request, expectAccessible);

  // The first workspace. Its slug follows the name.
  await expect(page.getByRole("heading", { level: 1, name: "Workspaces" })).toBeVisible();
  await expectAccessible(page);
  const workspace = `Payments ${run}`;
  await page.getByLabel("Name", { exact: true }).fill(workspace);
  await expect(page.getByLabel("Slug")).toHaveValue(`payments-${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();

  // A project in it.
  await expect(page.getByRole("heading", { level: 1, name: workspace })).toBeVisible();
  await expect(page.getByText("No projects yet")).toBeVisible();
  await expectAccessible(page);
  await page.getByLabel("Name", { exact: true }).fill("Shop");
  await page.getByLabel("Description (optional)").fill("Checkout, orders and payments.");
  await page.getByRole("button", { name: "Create project" }).click();

  // The project's canvas, still empty.
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
  await expect(page.getByText("No services on this canvas yet")).toBeVisible();
  await expect(page.locator(".react-flow")).toBeVisible();
  await expectAccessible(page);
  const projectURL = page.url();

  // Back to the workspace through the breadcrumb.
  await page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: workspace }).click();
  await expect(page.getByRole("link", { name: /Shop/ })).toBeVisible();

  // Spec checks work for a signed-in user.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Spec check" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Spec check" })).toBeVisible();
  await expect(page.getByText(/\d+ errors?, \d+ warnings?/)).toBeVisible();
  await expectAccessible(page);

  // An access token works without the browser's session, until it's revoked.
  await page.getByRole("link", { name: "Access tokens" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Access tokens" })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill(`CI ${run}`);
  await page.getByRole("button", { name: "Create token" }).click();
  const secret = await page.getByLabel("Token", { exact: true }).inputValue();
  expect(secret).toMatch(/^sbx_pat_/);
  await expectAccessible(page);
  await page.getByRole("button", { name: "Done" }).click();
  const asToken = { headers: { Authorization: `Bearer ${secret}` } };
  const me = await request.get("/api/v1/me", asToken);
  expect(me.status()).toBe(200);
  await page.getByRole("button", { name: `Revoke CI ${run}` }).click();
  await expect(page.getByRole("button", { name: `Revoke CI ${run}` })).toHaveCount(0);
  expect((await request.get("/api/v1/me", asToken)).status()).toBe(401);

  // After signing out, a link to the project asks for sign-in first and
  // then opens the project.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  await page.goto(projectURL);
  await expect(page).toHaveURL(/\/sign-in\?redirect=/);
  await expectAccessible(page);
  await signIn(page);
  await expect(page).toHaveURL(projectURL);
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
});

test("a page that can't load says why and can try again", async ({ page, request }) => {
  test.skip(await setupRequired(request), "needs an installation that is set up");
  // The session check fails as it would while the database is down.
  await page.route("**/api/v1/sessions/current", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/problem+json",
      body: JSON.stringify({ type: "about:blank", title: "Service Unavailable", status: 503, detail: "PostgreSQL didn't answer." }),
    }),
  );
  await page.goto("/workspaces");
  await expect(page.getByRole("heading", { level: 1, name: "This page couldn't load" })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText("PostgreSQL didn't answer.")).toBeVisible();
  await expectAccessible(page);

  await page.unroute("**/api/v1/sessions/current");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});

test("a wrong password is refused", async ({ page, request }) => {
  test.skip(await setupRequired(request), "needs an installation that is set up");
  await page.goto("/sign-in");
  await page.getByLabel("Email address").fill(admin.email);
  await page.getByLabel("Password").fill("not the password at all");
  await page.getByRole("button", { name: "Sign in" }).click();
  const alert = page.getByRole("alert");
  await expect(alert).toHaveText("The email address or password is wrong.");
  await expect(alert).toBeFocused();
});
