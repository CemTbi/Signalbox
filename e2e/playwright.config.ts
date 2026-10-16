// SPDX-License-Identifier: Apache-2.0

import { defineConfig, devices } from "@playwright/test";

// The tests drive a running installation, such as the Compose stack:
//
//   SIGNALBOX_SETUP_CODE=E2E-SETUP-CODE-0001 docker compose -f deploy/compose/compose.yaml up -d --wait
//   SIGNALBOX_E2E_SETUP_CODE=E2E-SETUP-CODE-0001 npm --prefix e2e test
//
// Against a new installation they complete first-run setup; against one that
// is already set up they sign in with SIGNALBOX_E2E_EMAIL and
// SIGNALBOX_E2E_PASSWORD.

export default defineConfig({
  testDir: "tests",
  // The tests share one installation and run in order.
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: process.env.SIGNALBOX_E2E_BASE_URL ?? "http://localhost:8080",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
