// SPDX-License-Identifier: Apache-2.0

// Builds the canvas benchmark as production code, which is what spike 3
// measures: npm run -w @signalbox/app bench:build, then bench:serve.
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  root: fileURLToPath(new URL("..", import.meta.url)),
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("../node_modules/.bench", import.meta.url)),
    emptyOutDir: true,
    rollupOptions: { input: fileURLToPath(new URL("./canvas.html", import.meta.url)) },
  },
  preview: { port: 4174, strictPort: true },
});
