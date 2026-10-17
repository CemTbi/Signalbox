// SPDX-License-Identifier: Apache-2.0

// Bundles pm.js and the libraries Postman scripts use into sandbox.js, which
// the Go package embeds, and writes THIRD_PARTY_LICENSES.txt for the code it
// bundles. Fails if a bundled package's license is outside ADR-0008's list.
//
//   npm --prefix internal/modules/scripts/sandbox ci
//   npm --prefix internal/modules/scripts/sandbox run build

import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { build } from "esbuild";

const require = createRequire(import.meta.url);

// ADR-0008, as amended by ADR-0011 and ADR-0015. Keep in step with
// web/scripts/check-licenses.mjs.
const allowed = new Set(["MIT", "MIT-0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "Apache-2.0", "0BSD", "Zlib", "Unlicense", "CC0-1.0"]);

const result = await build({
  entryPoints: ["entry.js"],
  bundle: true,
  minify: true,
  format: "iife",
  platform: "browser",
  target: "es2020",
  outfile: "sandbox.js",
  legalComments: "none", // THIRD_PARTY_LICENSES.txt has them all
  metafile: true,
  logLevel: "warning",
  inject: ["./globals.js"],
  plugins: [
    {
      // Every import of faker, including its locales, gets the stub.
      name: "no-faker",
      setup(b) {
        b.onResolve({ filter: /^@faker-js\/faker(\/|$)/ }, () => ({ path: path.resolve("faker-stub.js") }));
        // Node's built-in modules, for libraries written for Node.
        const builtins = { timers: "timers-browserify", stream: "stream-browserify" };
        b.onResolve({ filter: /^(timers|stream)$/ }, (args) => ({ path: require.resolve(builtins[args.path]) }));
      },
    },
  ],
});

// The packages whose code is in the bundle.
const packages = new Map();
for (const [input, info] of Object.entries(result.metafile.inputs)) {
  // A file a package disables for browsers adds no code.
  if (input.startsWith("(disabled):") || info.bytes === 0) continue;
  const m = input.match(/node_modules\/((?:@[^/]+\/)?[^/]+)\//g);
  if (!m) continue;
  const dir = m.at(-1).replace(/\/$/, ""); // the innermost node_modules/<pkg>
  const base = input.slice(0, input.lastIndexOf(dir) + dir.length);
  if (!packages.has(base)) packages.set(base, JSON.parse(readFileSync(path.join(base, "package.json"), "utf8")));
}

function licenseOf(pkg) {
  if (typeof pkg.license === "string") return pkg.license;
  if (Array.isArray(pkg.license)) return pkg.license.map((l) => l.type ?? l).join(" OR ");
  if (pkg.license?.type) return pkg.license.type;
  if (Array.isArray(pkg.licenses)) return pkg.licenses.map((l) => l.type ?? l).join(" OR ");
  return "UNKNOWN";
}

function permitted(expression) {
  return expression
    .replace(/[()]/g, " ")
    .split(/\s+OR\s+/)
    .some((choice) => choice.split(/\s+AND\s+/).every((id) => allowed.has(id.trim())));
}

const refused = [];
const notices = [];
for (const [dir, pkg] of [...packages].sort((a, b) => a[1].name.localeCompare(b[1].name))) {
  const license = licenseOf(pkg);
  if (!permitted(license)) refused.push(`${pkg.name}@${pkg.version}: ${license}`);
  const file = readdirSync(dir).find((f) => /^(licen[cs]e|copying)(\.|$)/i.test(f));
  // Some packages ship license files with Windows line endings.
  const text = file
    ? readFileSync(path.join(dir, file), "utf8").replace(/\r\n?/g, "\n").trim()
    : `Licensed under ${license}; the package ships no license file.`;
  notices.push(`${pkg.name} ${pkg.version} (${license})\n\n${text}`);
}
if (refused.length > 0) {
  console.error("These bundled packages have licenses outside the allowlist (ADR-0008):");
  for (const r of refused) console.error(`  ${r}`);
  process.exit(1);
}
writeFileSync(
  "THIRD_PARTY_LICENSES.txt",
  "Signalbox's script sandbox (sandbox.js) bundles these packages.\n\n" + notices.join("\n\n" + "-".repeat(72) + "\n\n") + "\n",
);
const bytes = Object.values(result.metafile.outputs)[0].bytes;
console.log(`sandbox.js: ${(bytes / 1e6).toFixed(2)} MB, ${packages.size} packages, all licenses allowed`);
