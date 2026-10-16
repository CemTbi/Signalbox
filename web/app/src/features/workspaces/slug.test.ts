// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { MAX_SLUG, slugify } from "./slug";

describe("slugify", () => {
  it.each([
    ["Payments", "payments"],
    ["Payments Team", "payments-team"],
    ["  Orders & Billing!! ", "orders-billing"],
    ["Zahlungsverkehr Übersee", "zahlungsverkehr-ubersee"],
    ["Café 2.0", "cafe-2-0"],
    ["---", ""],
    ["日本語", ""],
  ])("%s → %s", (name, want) => {
    expect(slugify(name)).toBe(want);
  });

  it("keeps slugs within the API's limit without a trailing hyphen", () => {
    const slug = slugify(`${"a".repeat(62)} b`);
    expect(slug.length).toBeLessThanOrEqual(MAX_SLUG);
    expect(slug.endsWith("-")).toBe(false);
  });

  it("produces slugs the API accepts", () => {
    for (const name of ["Payments", "Orders & Billing", "Café 2.0", "x".repeat(100)]) {
      expect(slugify(name)).toMatch(/^[a-z0-9]+(?:-[a-z0-9]+)*$/);
    }
  });
});
