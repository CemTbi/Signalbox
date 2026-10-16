// SPDX-License-Identifier: Apache-2.0

import { admin, expect, expectAccessible, openAsAdmin, runID, test } from "./support";

// Bringing a second person in: the owner creates an invitation link, the
// invitee opens it in their own browser, creates an account and lands in
// the workspace with the role they were given. No email is sent.

test("an invited person joins with a new account", async ({ page, request, browser }) => {
  const run = runID();
  await openAsAdmin(page, request);
  const workspace = `Team ${run}`;
  await page.getByLabel("Name", { exact: true }).fill(workspace);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: workspace })).toBeVisible();

  await page.getByRole("navigation", { name: "Workspace" }).getByRole("link", { name: "Members" }).click();
  await expect(page.getByRole("heading", { level: 2, name: "Members" })).toBeVisible();
  await expect(page.getByText(`${admin.name} (you)`)).toBeVisible();
  await expectAccessible(page);

  const graceEmail = `grace-${run}@example.com`;
  await page.getByLabel("Email address").fill(graceEmail);
  await page.getByLabel("Role", { exact: true }).selectOption("editor");
  await page.getByRole("button", { name: "Create invitation link" }).click();
  const link = page.getByLabel("Invitation link");
  await expect(link).toHaveValue(/\/invite#[A-Za-z0-9_-]{43}$/);
  const url = await link.inputValue();
  await expectAccessible(page);
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByText(graceEmail)).toBeVisible(); // under open invitations

  // Grace opens the link in her own browser.
  const graceContext = await browser.newContext();
  const grace = await graceContext.newPage();
  await grace.goto(url);
  await expect(grace.getByRole("heading", { name: `Join ${workspace}` })).toBeVisible();
  await expect(grace.getByText(`${admin.name} invited`)).toContainText("as an editor");
  await expectAccessible(grace);
  await grace.getByLabel("Your name").fill("Grace Hopper");
  await grace.getByLabel("Password").fill("correct horse battery staple");
  await grace.getByRole("button", { name: "Create account and join" }).click();
  await expect(grace.getByRole("heading", { level: 1, name: workspace })).toBeVisible();
  await expect(grace.getByText("Editor", { exact: true })).toBeVisible();

  // The link worked once.
  const reuse = await graceContext.newPage();
  await reuse.goto(url);
  await expect(reuse.getByRole("heading", { name: "This invitation can't be used" })).toBeVisible();
  await graceContext.close();

  // Back with the owner: Grace is a member now, and her role can change.
  await page.reload();
  const role = page.getByLabel("Role of Grace Hopper");
  await expect(role).toHaveValue("editor");
  await role.selectOption("viewer");
  await expect(role).toHaveValue("viewer");
  await expect(page.getByText(graceEmail, { exact: true })).toHaveCount(1); // no longer an open invitation

  // An invitation can be revoked before anyone uses it.
  await page.getByLabel("Email address").fill(`someone-${run}@example.com`);
  await page.getByRole("button", { name: "Create invitation link" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: `Revoke the invitation for someone-${run}@example.com` }).click();
  await expect(page.getByText(`someone-${run}@example.com`)).toHaveCount(0);
});
