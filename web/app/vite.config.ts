// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type ProxyOptions } from "vite";
import type { ViteUserConfig } from "vitest/config";

// `npm run dev` proxies API calls to a signalbox-server started with
// `make run`, whose public URL is this address.
const server = process.env.SIGNALBOX_DEV_SERVER ?? "http://localhost:8080";

const port = 5173;
const devOrigins = new Set([`http://localhost:${port}`, `http://127.0.0.1:${port}`]);

// The server only accepts unsafe requests from its own origin. Requests
// from this dev server's pages come from another port, so the proxy
// presents them as coming from the server's origin. Other origins pass
// through unchanged and are refused as usual.
const proxy: ProxyOptions = {
  target: server,
  configure: (p) => {
    p.on("proxyReq", (req) => {
      const origin = req.getHeader("origin");
      if (typeof origin === "string" && devOrigins.has(origin)) {
        req.setHeader("origin", new URL(server).origin);
      }
    });
  },
};

export default defineConfig({
  plugins: [react()],
  build: {
    // The Go server embeds this directory; see internal/webui.
    outDir: fileURLToPath(new URL("../../internal/webui/dist", import.meta.url)),
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    port,
    proxy: {
      "/api": proxy,
      "/livez": proxy,
      "/readyz": proxy,
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
} satisfies ViteUserConfig);
