// SPDX-License-Identifier: Apache-2.0

/**
 * Heartbeat tokens just made, by monitor, so the page a new monitor opens
 * on can show its token once. They live in memory only: a reload loses
 * them, as the API means it to, and they never reach storage.
 */
const fresh = new Map<string, string>();

export function keepFreshToken(monitorId: string, token: string) {
  fresh.set(monitorId, token);
}

/** The token made for a monitor, if this page made one, and forgets it. */
export function takeFreshToken(monitorId: string): string | undefined {
  const token = fresh.get(monitorId);
  fresh.delete(monitorId);
  return token;
}
