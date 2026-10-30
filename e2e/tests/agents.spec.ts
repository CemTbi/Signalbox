// SPDX-License-Identifier: Apache-2.0

import { expectAccessible, openAsAdmin, runID, test, expect } from "./support";

test("a request sent through an agent", async ({ page, request }) => {
  const run = runID();
  await openAsAdmin(page, request);

  await page.getByLabel("Name", { exact: true }).fill(`Local machines ${run}`);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Local machines ${run}` })).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Local");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/prj_[0-9a-z]+$/);
  await expect(page.getByRole("heading", { level: 1, name: "Local" })).toBeVisible();
  const projectURL = page.url();

  // The agent's token, and the agent connecting with it as `signalbox agent` does.
  await page.getByRole("link", { name: "Access tokens" }).click();
  await page.getByLabel("Name", { exact: true }).fill(`Agent ${run}`);
  await page.getByRole("button", { name: "Create token" }).click();
  const secret = await page.getByLabel("Token", { exact: true }).inputValue();
  await page.getByRole("button", { name: "Done" }).click();
  const asAgent = { Authorization: `Bearer ${secret}` };
  const name = `laptop-${run}`;
  const connected = await request.post("/api/v1/agents", { headers: asAgent, data: { name, os: "linux/amd64", version: "e2e" } });
  expect(connected.status()).toBe(200);
  const agent = (await connected.json()) as { id: string };

  await page.getByRole("link", { name: "Agents", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Agents" })).toBeVisible();
  await expect(page.getByText(name)).toBeVisible();
  await expect(page.getByText("signalbox agent --server")).toBeVisible();
  await expectAccessible(page);

  // The agent answers the next job as if from its machine.
  const answered = (async () => {
    for (let i = 0; i < 20; i++) {
      const next = await request.post(`/api/v1/agents/${agent.id}/jobs/next?waitMs=2000`, { headers: asAgent });
      if (next.status() === 204) continue;
      expect(next.status()).toBe(200);
      const job = (await next.json()) as { id: string; url: string };
      const done = await request.put(`/api/v1/agents/${agent.id}/jobs/${job.id}`, {
        headers: asAgent,
        data: {
          response: {
            status: 200,
            statusText: "200 OK",
            headers: [{ key: "Content-Type", value: "text/plain" }],
            body: Buffer.from(`hello from ${name}`).toString("base64"),
            url: job.url,
          },
          timings: { dnsMs: 0, connectMs: 0.2, tlsMs: 0, firstByteMs: 1, downloadMs: 0.1, totalMs: 1.3 },
        },
      });
      expect(done.status()).toBe(204);
      return job.url;
    }
    throw new Error("the agent got no job");
  })();

  await page.goto(`${projectURL}/requests`);
  await expect(page.getByRole("heading", { level: 1, name: "Requests" })).toBeVisible();
  await page.getByLabel("Run on").selectOption(agent.id);
  await page.getByLabel("URL").fill("http://localhost:3000/health");
  await page.getByRole("button", { name: "Send" }).click();
  const response = page.getByRole("region", { name: "Response" });
  await expect(response.getByText(`hello from ${name}`)).toBeVisible();
  await expect(response.getByText(`from ${name}`, { exact: false }).first()).toBeVisible();
  expect(await answered).toBe("http://localhost:3000/health");
  await expectAccessible(page);

  // Deleted, it no longer appears.
  await page.getByRole("link", { name: "Agents", exact: true }).click();
  await page.getByRole("button", { name: `Delete ${name}` }).click();
  await expect(page.getByRole("button", { name: `Delete ${name}` })).toHaveCount(0);
});
