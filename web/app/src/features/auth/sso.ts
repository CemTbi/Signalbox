// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from "@signalbox/api-client";
import { queryOptions } from "@tanstack/react-query";
import { unwrap } from "../../api";

/** How people can sign in besides a password. */
export function signInOptionsQuery(client: ApiClient) {
  return queryOptions({
    queryKey: ["sign-in-options"],
    queryFn: () => unwrap(client.GET("/api/v1/sign-in-options")),
    staleTime: 5 * 60_000,
  });
}

const STASH_KEY = "signalbox.sso.return";

/** Returns the address that starts single sign-on and comes back to returnTo's path. */
export function ssoStartURL(returnTo: string): string {
  const path = returnTo.split("#", 1)[0] || "/";
  return `/api/v1/sso/start?${new URLSearchParams({ return: path })}`;
}

/**
 * Keeps the part of returnTo after "#", such as an invitation token, in
 * this tab's session storage while the browser is at the identity
 * provider, so it never goes to the server. Call it when the sign-in
 * starts; takeStashedHash picks it up on the way back.
 */
export function rememberReturnHash(returnTo: string): void {
  const at = returnTo.indexOf("#");
  if (at < 0) return;
  try {
    sessionStorage.setItem(STASH_KEY, JSON.stringify({ path: returnTo.slice(0, at) || "/", hash: returnTo.slice(at + 1) }));
  } catch {
    // Without session storage the part after # is lost, and the page asks
    // for the link again.
  }
}

/** Returns, once, the hash remembered by rememberReturnHash for this path. */
export function takeStashedHash(path: string): string | undefined {
  try {
    const raw = sessionStorage.getItem(STASH_KEY);
    if (!raw) return undefined;
    sessionStorage.removeItem(STASH_KEY);
    const stashed = JSON.parse(raw) as { path?: unknown; hash?: unknown };
    return stashed.path === path && typeof stashed.hash === "string" ? stashed.hash : undefined;
  } catch {
    return undefined;
  }
}

/** Explains why single sign-on failed, from the reason the server sends. */
export const ssoFailureMessages: Record<string, string> = {
  expired: "The sign-in took too long or was already used. Try again.",
  failed: "Single sign-on didn't work. Try again; if it keeps failing, ask your administrator to check the server log.",
  denied: "The sign-in was cancelled at the identity provider.",
  unavailable: "The identity provider can't be reached right now. Try again later, or sign in with a password.",
  "email-unverified":
    "Your identity provider didn't confirm your email address, so Signalbox can't create an account for you. Ask your administrator.",
  "not-allowed": "Accounts for your email domain can't be created here. Ask your administrator for access.",
  "account-exists": "An account with your email address already exists. Sign in with its password.",
  "rate-limited": "Too many sign-in attempts. Wait a minute and try again.",
};
