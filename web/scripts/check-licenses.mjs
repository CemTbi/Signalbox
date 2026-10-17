// SPDX-License-Identifier: Apache-2.0

// Fails when a package that ships in the web app has a license outside the
// allowlist of ADR-0008, as amended by ADR-0011. Development tools aren't
// shipped, so only production dependencies are checked.
//
//   npm --prefix web run licenses

import { execFileSync } from "node:child_process";

const allowed = new Set([
  "MIT",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "ISC",
  "Apache-2.0",
  "0BSD",
  "MIT-0", // ADR-0015
  "Zlib",
  "Unicode-3.0",
  "Unicode-DFS-2016",
  "MPL-2.0",
  "EPL-2.0",
  "OFL-1.1",
  // Public-domain dedications (ADR-0011).
  "Unlicense",
  "CC0-1.0",
]);

/** Reads the license of a package.json, old formats included. */
function licenseOf(pkg) {
  if (typeof pkg.license === "string") return pkg.license;
  if (Array.isArray(pkg.license)) return pkg.license.map((l) => l.type ?? l).join(" OR ");
  if (pkg.license?.type) return pkg.license.type;
  if (Array.isArray(pkg.licenses)) return pkg.licenses.map((l) => l.type ?? l).join(" OR ");
  return "UNKNOWN";
}

/** Checks an SPDX expression: one allowed choice of an OR, all parts of an AND. */
function permitted(expression) {
  return expression
    .replace(/[()]/g, " ")
    .split(/\s+OR\s+/)
    .some((choice) => choice.split(/\s+AND\s+/).every((id) => allowed.has(id.trim())));
}

// Under `npm run`, npm_execpath is npm's own script, which runs with node on
// every platform; a plain "npm" would need a shell on Windows.
const npm = process.env.npm_execpath
  ? [process.execPath, [process.env.npm_execpath, "query", ".prod"]]
  : ["npm", ["query", ".prod"]];
const output = execFileSync(npm[0], npm[1], { encoding: "utf8", maxBuffer: 64 << 20 });
const packages = JSON.parse(output).filter((pkg) => !pkg.private);

const refused = packages
  .map((pkg) => ({ id: `${pkg.name}@${pkg.version}`, license: licenseOf(pkg) }))
  .filter(({ license }) => !permitted(license));

if (refused.length > 0) {
  console.error("These shipped dependencies have licenses outside the allowlist (ADR-0008):");
  for (const { id, license } of refused) console.error(`  ${id}: ${license}`);
  process.exit(1);
}
console.log(`Checked the licenses of ${packages.length} shipped packages: all allowed.`);
