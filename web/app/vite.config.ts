// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// `npm run dev` proxies API calls to a signalbox-server started with `make run`.
const server = process.env.SIGNALBOX_DEV_SERVER ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  build: {
    // The Go server embeds this directory; see internal/webui.
    outDir: fileURLToPath(new URL("../../internal/webui/dist", import.meta.url)),
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": server,
      "/livez": server,
      "/readyz": server,
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
