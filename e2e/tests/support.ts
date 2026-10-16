// SPDX-License-Identifier: Apache-2.0

import AxeBuilder from "@axe-core/playwright";
import { type APIRequestContext, test as base, expect, type Page } from "@playwright/test";

export { expect };

// Every page must work under the server's Content Security Policy and
// without uncaught errors. Failed requests, such as the 401 that tells the
// app nobody is signed in, are expected and aren't collected. The check
// covers every page the test opens, in every browser context.
export const test = base.extend<{ cleanConsole: void }>({
  cleanConsole: [
    async ({ context }, use) => {
      const problems: string[] = [];
      const watch = (page: Page) => {
        page.on("pageerror", (error) => problems.push(`uncaught error: ${error.message}`));
        page.on("console", (message) => {
          const text = message.text();
          if (message.type() === "error" && /Content Security Policy|Refused to/i.test(text)) {
            problems.push(`blocked by the Content Security Policy: ${text}`);
          }
        });
      };
      context.pages().forEach(watch);
      context.on("page", watch);
      await use();
      expect(problems, "the browser reported problems").toEqual([]);
    },
    { auto: true },
  ],
});

export const admin = {
  name: "Ada Lovelace",
  email: process.env.SIGNALBOX_E2E_EMAIL ?? "ada@example.com",
  password: process.env.SIGNALBOX_E2E_PASSWORD ?? "correct horse battery staple",
};
export const setupCode = process.env.SIGNALBOX_E2E_SETUP_CODE ?? "";

export async function setupRequired(request: APIRequestContext): Promise<boolean> {
  const response = await request.get("/api/v1/setup");
  expect(response.ok()).toBe(true);
  return ((await response.json()) as { required: boolean }).required;
}

/** Fails when axe-core finds WCAG 2.2 AA violations on the page (ADR-0006). */
export async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  const summary = results.violations
    .map((v) => `${v.id} (${v.impact}): ${v.help}\n  ${v.nodes.map((n) => n.target.join(" ")).join("\n  ")}`)
    .join("\n");
  expect(results.violations, `accessibility violations on ${page.url()}:\n${summary}`).toEqual([]);
}

/** Signs in on the sign-in page. */
export async function signIn(page: Page, email = admin.email, password = admin.password) {
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

/**
 * Opens Signalbox as the admin: completes first-run setup on a new
 * installation, or signs in on one that is set up. Ends on the workspaces
 * page. Returns whether it ran setup.
 */
export async function openAsAdmin(page: Page, request: APIRequestContext, check?: (page: Page) => Promise<void>) {
  await page.goto("/");
  const setUp = await setupRequired(request);
  if (setUp) {
    test.skip(!setupCode, "set SIGNALBOX_E2E_SETUP_CODE to run against a new installation");
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole("heading", { name: "Set up Signalbox" })).toBeVisible();
    await check?.(page);
    await page.getByLabel("Setup code").fill(setupCode);
    await page.getByLabel("Your name").fill(admin.name);
    await page.getByLabel("Email address").fill(admin.email);
    await page.getByLabel("Password").fill(admin.password);
    await page.getByRole("button", { name: "Create admin account" }).click();
  } else {
    await expect(page).toHaveURL(/\/sign-in/);
    await signIn(page);
  }
  await expect(page).toHaveURL(/\/workspaces$/);
  return setUp;
}

/** A suffix that keeps names unique when the tests run again on one installation. */
export function runID() {
  return Date.now().toString(36);
}
