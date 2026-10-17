// SPDX-License-Identifier: Apache-2.0

import type { Page } from "@playwright/test";
import { admin, expect, expectAccessible, openAsAdmin, runID, setupRequired, test } from "./support";

// Single sign-on through Dex, a real OpenID Connect provider, which
// deploy/compose/compose.e2e.yaml adds to the stack. Its people are in
// deploy/compose/dex/e2e.yaml. The tests run in order: the first one gives
// Lin an account, which the second one uses.

const lin = { name: "Lin Okafor", email: "lin@example.com" };
const dexPassword = "correct horse battery staple";

test.describe.configure({ mode: "serial" });

test.beforeEach(async ({ request }) => {
  const response = await request.get("/api/v1/sign-in-options");
  expect(response.ok()).toBe(true);
  const options = (await response.json()) as { sso?: { name: string } };
  test.skip(options.sso?.name !== "Dex", "start the stack with deploy/compose/compose.e2e.yaml to test single sign-on");
});

/** Signs in on Dex's password page, where the sign-in link leads. */
async function signInAtDex(page: Page, email: string) {
  await page.waitForURL(/^http:\/\/dex:5556\/dex\//);
  await page.locator("#login").fill(email);
  await page.locator("#password").fill(dexPassword);
  await page.locator("#submit-login").click();
}

async function currentUserID(page: Page) {
  const response = await page.request.get("/api/v1/sessions/current");
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { user: { id: string } }).user.id;
}

test("someone signs in with single sign-on and keeps one account", async ({ page, browser, request }) => {
  test.skip(await setupRequired(request), "needs an installation that is set up");
  await page.goto("/sign-in");
  const signInWithDex = page.getByRole("link", { name: "Sign in with Dex" });
  await expect(signInWithDex).toBeVisible();
  await expectAccessible(page);
  await signInWithDex.click();
  await signInAtDex(page, lin.email);

  // The first sign-in creates Lin's account, named by Dex.
  await expect(page).toHaveURL(/\/workspaces$/);
  await expect(page.getByTitle(lin.email)).toHaveText(lin.name);
  const id = await currentUserID(page);

  // Signing in again, from another browser, finds the same account.
  const otherBrowser = await browser.newContext();
  const again = await otherBrowser.newPage();
  await again.goto("/sign-in");
  await again.getByRole("link", { name: "Sign in with Dex" }).click();
  await signInAtDex(again, lin.email);
  await expect(again).toHaveURL(/\/workspaces$/);
  expect(await currentUserID(again)).toBe(id);
  await otherBrowser.close();
});

test("an invitation link keeps working through single sign-on", async ({ page, browser, request }) => {
  const run = runID();
  await openAsAdmin(page, request);
  const workspace = `Platform ${run}`;
  await page.getByLabel("Name", { exact: true }).fill(workspace);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: workspace })).toBeVisible();
  await page.getByRole("navigation", { name: "Workspace" }).getByRole("link", { name: "Members" }).click();
  await page.getByLabel("Email address").fill(lin.email);
  await page.getByLabel("Role", { exact: true }).selectOption("editor");
  await page.getByRole("button", { name: "Create invitation link" }).click();
  const url = await page.getByLabel("Invitation link").inputValue();

  // Lin has an account from the test before, so the invitation asks Lin to
  // sign in. The token after # mustn't reach the server or Dex, and must
  // still be there on the way back.
  const linBrowser = await browser.newContext();
  const linPage = await linBrowser.newPage();
  await linPage.goto(url);
  await expect(linPage.getByRole("heading", { name: `Join ${workspace}` })).toBeVisible();
  await linPage.getByRole("link", { name: "Sign in", exact: true }).click();
  await linPage.getByRole("link", { name: "Sign in with Dex" }).click();
  await signInAtDex(linPage, lin.email);
  await expect(linPage).toHaveURL(/\/invite$/);
  await linPage.getByRole("button", { name: `Join ${workspace}` }).click();
  await expect(linPage.getByRole("heading", { level: 1, name: workspace })).toBeVisible();
  await expect(linPage.getByText("Editor", { exact: true })).toBeVisible();
  await linBrowser.close();
});

test("single sign-on never takes over an account with the same email address", async ({ page, request }) => {
  test.skip(admin.email !== "ada@example.com", "Dex knows the admin only as ada@example.com");
  test.skip(await setupRequired(request), "needs an installation that is set up");
  await page.goto("/sign-in");
  await page.getByRole("link", { name: "Sign in with Dex" }).click();
  await signInAtDex(page, admin.email);

  // Ada's Signalbox account has a password and isn't linked to Dex.
  await expect(page).toHaveURL(/\/sign-in\?sso=account-exists$/);
  await expect(page.getByRole("alert")).toHaveText(
    "An account with your email address already exists. Sign in with its password.",
  );
  await expectAccessible(page);
});
