// SPDX-License-Identifier: Apache-2.0

import type { ApiClient, Session } from "@signalbox/api-client";
import { type QueryClient, queryOptions } from "@tanstack/react-query";
import { ApiError, setCsrfToken, unwrap } from "./api";

export const sessionKey = ["session"] as const;
export const setupKey = ["setup"] as const;

/** The signed-in session, or null when nobody is signed in. */
export function sessionQuery(client: ApiClient) {
  return queryOptions({
    queryKey: sessionKey,
    queryFn: async (): Promise<Session | null> => {
      try {
        const session = await unwrap(client.GET("/api/v1/sessions/current"));
        setCsrfToken(session.csrfToken);
        return session;
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          setCsrfToken(undefined);
          return null;
        }
        throw error;
      }
    },
    staleTime: 5 * 60_000,
  });
}

/** Whether the installation still needs its first admin account. */
export function setupStatusQuery(client: ApiClient) {
  return queryOptions({
    queryKey: setupKey,
    queryFn: () => unwrap(client.GET("/api/v1/setup")),
    staleTime: 60_000,
  });
}

/** Stores a session that sign-in or setup just created. */
export function storeSession(queryClient: QueryClient, session: Session) {
  setCsrfToken(session.csrfToken);
  queryClient.setQueryData(sessionKey, session);
  queryClient.setQueryData(setupKey, { required: false });
}

/** Forgets the session and everything loaded with it. */
export function forgetSession(queryClient: QueryClient) {
  setCsrfToken(undefined);
  queryClient.clear();
  queryClient.setQueryData(sessionKey, null);
}

/**
 * Returns the path to go back to after signing in, if it is a path on this
 * site. Anything else, such as "//evil.example", is ignored, so the
 * parameter can't send people to another site.
 */
export function safeRedirect(value: unknown): string | undefined {
  if (typeof value !== "string" || !value.startsWith("/") || value.startsWith("//") || value.startsWith("/\\")) {
    return undefined;
  }
  return value;
}
