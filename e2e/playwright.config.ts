// SPDX-License-Identifier: Apache-2.0

import { defineConfig, devices } from "@playwright/test";

// The tests drive a running installation, such as the Compose stack with the
// Dex identity provider for the single sign-on tests:
//
//   export COMPOSE_FILE=deploy/compose/compose.yaml:deploy/compose/compose.e2e.yaml
//   SIGNALBOX_SETUP_CODE=E2E-SETUP-CODE-0001 docker compose up -d --wait
//   SIGNALBOX_E2E_SETUP_CODE=E2E-SETUP-CODE-0001 npm --prefix e2e test
//
// (On Windows, separate the files in COMPOSE_FILE with ";".) Against a new
// installation they complete first-run setup; against one that is already
// set up they sign in with SIGNALBOX_E2E_EMAIL and SIGNALBOX_E2E_PASSWORD.
// Without Dex, the single sign-on tests are skipped.

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
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // The server reaches Dex as dex:5556 on the Compose network, and the
        // browser must use the same address, the issuer's.
        launchOptions: { args: ["--host-resolver-rules=MAP dex 127.0.0.1"] },
      },
    },
  ],
});
