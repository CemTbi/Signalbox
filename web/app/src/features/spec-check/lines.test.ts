// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { lineRange, summarize } from "./lines";

describe("lineRange", () => {
  const text = "first\nsecond\n\nfourth";

  it.each([
    [1, 0, 5],
    [2, 6, 12],
    [3, 13, 13],
    [4, 14, 20],
    [9, 20, 20],
  ])("line %i spans %i to %i", (line, start, end) => {
    expect(lineRange(text, line)).toEqual({ start, end });
  });
});

describe("summarize", () => {
  it("matches the CLI's wording", () => {
    expect(summarize({ errors: 1, warnings: 5, infos: 0, hints: 2 })).toBe("1 error, 5 warnings, 0 info, 2 hints");
  });

  it("says when there is nothing to report", () => {
    expect(summarize({ errors: 0, warnings: 0, infos: 0, hints: 0 })).toBe("No problems found");
  });
});
