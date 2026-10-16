// SPDX-License-Identifier: Apache-2.0

/** The longest slug the API accepts. */
export const MAX_SLUG = 63;

/**
 * Suggests a slug for a name: lowercase ASCII letters and digits joined by
 * single hyphens, as the API requires. Accents are dropped, so "Zahlungen
 * Ü" becomes "zahlungen-u".
 */
export function slugify(name: string): string {
  return name
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_SLUG)
    .replace(/-+$/, "");
}
