// SPDX-License-Identifier: Apache-2.0

import AxeBuilder from "@axe-core/playwright";
import { type APIRequestContext, test as base, expect, type Page } from "@playwright/test";

// Every page must work under the server's Content Security Policy and
// without uncaught errors. Failed requests, such as the 401 that tells the
// app nobody is signed in, are expected and aren't collected.
const test = base.extend<{ cleanConsole: void }>({
  cleanConsole: [
    async ({ page }, use) => {
      const problems: string[] = [];
      page.on("pageerror", (error) => problems.push(`uncaught error: ${error.message}`));
      page.on("console", (message) => {
        const text = message.text();
        if (message.type() === "error" && /Content Security Policy|Refused to/i.test(text)) {
          problems.push(`blocked by the Content Security Policy: ${text}`);
        }
      });
      await use();
      expect(problems, "the browser reported problems").toEqual([]);
    },
    { auto: true },
  ],
});

// The walking skeleton's promise: from a new installation, someone can set
// it up, sign in, create a workspace and a project, and see the project's
// canvas. Every page on the way must pass an automated accessibility check
// against WCAG 2.2 AA (ADR-0006).

const admin = {
  name: "Ada Lovelace",
  email: process.env.SIGNALBOX_E2E_EMAIL ?? "ada@example.com",
  password: process.env.SIGNALBOX_E2E_PASSWORD ?? "correct horse battery staple",
};
const setupCode = process.env.SIGNALBOX_E2E_SETUP_CODE ?? "";

async function setupRequired(request: APIRequestContext): Promise<boolean> {
  const response = await request.get("/api/v1/setup");
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { required: boolean }).required;
}

async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  const summary = results.violations
    .map((v) => `${v.id} (${v.impact}): ${v.help}\n  ${v.nodes.map((n) => n.target.join(" ")).join("\n  ")}`)
    .join("\n");
  expect(results.violations, `accessibility violations on ${page.url()}:\n${summary}`).toEqual([]);
}

async function signIn(page: Page) {
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await page.getByLabel("Email address").fill(admin.email);
  await page.getByLabel("Password").fill(admin.password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test("from a new installation to a project's canvas", async ({ page, request }) => {
  const run = Date.now().toString(36);

  await page.goto("/");
  if (await setupRequired(request)) {
    test.skip(!setupCode, "set SIGNALBOX_E2E_SETUP_CODE to run against a new installation");
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole("heading", { name: "Set up Signalbox" })).toBeVisible();
    await expectAccessible(page);
    await page.getByLabel("Setup code").fill(setupCode);
    await page.getByLabel("Your name").fill(admin.name);
    await page.getByLabel("Email address").fill(admin.email);
    await page.getByLabel("Password").fill(admin.password);
    await page.getByRole("button", { name: "Create admin account" }).click();
  } else {
    await expect(page).toHaveURL(/\/sign-in/);
    await signIn(page);
  }

  // The first workspace. Its slug follows the name.
  await expect(page).toHaveURL(/\/workspaces$/);
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
