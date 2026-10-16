// SPDX-License-Identifier: Apache-2.0

/**
 * Returns the character offsets where a 1-based line starts and ends, so a
 * textarea can select it. Lines past the end map to the end of the text.
 */
export function lineRange(text: string, line: number): { start: number; end: number } {
  let start = 0;
  for (let current = 1; current < line; current++) {
    const next = text.indexOf("\n", start);
    if (next === -1) {
      return { start: text.length, end: text.length };
    }
    start = next + 1;
  }
  const end = text.indexOf("\n", start);
  return { start, end: end === -1 ? text.length : end };
}

/** Formats counts the same way the CLI does: "1 error, 5 warnings, 0 info, 0 hints". */
export function summarize(counts: { errors: number; warnings: number; infos: number; hints: number }): string {
  const { errors, warnings, infos, hints } = counts;
  if (errors + warnings + infos + hints === 0) {
    return "No problems found";
  }
  const plural = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;
  return `${plural(errors, "error")}, ${plural(warnings, "warning")}, ${infos} info, ${plural(hints, "hint")}`;
}
