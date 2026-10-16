// SPDX-License-Identifier: Apache-2.0

import { type ApiClient, CSRF_HEADER, createApiClient, type Problem } from "@signalbox/api-client";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

/**
 * The session's CSRF token. The session cookie is HttpOnly, so scripts can't
 * read it; the token comes from the session response instead.
 */
let csrfToken: string | undefined;

/** Remembers the CSRF token that unsafe requests must carry. */
export function setCsrfToken(token: string | undefined) {
  csrfToken = token;
}

const sessionEndedListeners = new Set<() => void>();

/** Calls listener whenever a request finds that the session has ended. */
export function onSessionEnded(listener: () => void): () => void {
  sessionEndedListeners.add(listener);
  return () => sessionEndedListeners.delete(listener);
}

/**
 * Adds the CSRF token to unsafe requests, and reports a 401 from any
 * endpoint other than the session ones, which means the session expired or
 * was ended elsewhere.
 */
export function installSessionMiddleware(client: ApiClient) {
  client.use({
    onRequest({ request }) {
      if (csrfToken && !SAFE_METHODS.has(request.method)) {
        request.headers.set(CSRF_HEADER, csrfToken);
      }
      return request;
    },
    onResponse({ request, response }) {
      const path = new URL(request.url).pathname;
      if (response.status === 401 && !path.startsWith("/api/v1/sessions") && path !== "/api/v1/setup") {
        for (const listener of sessionEndedListeners) listener();
      }
      return response;
    },
  });
}

/** The app's shared API client. Components accept another one in tests. */
export const api = createApiClient();
installSessionMiddleware(api);

/** An error response from the API, with its RFC 9457 problem document. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem;

  constructor(status: number, problem: Problem) {
    super(problem.detail ?? problem.title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }

  /** The stable problem code, such as "slug-taken". */
  get code(): string | undefined {
    return this.problem.code;
  }

  /** The message for one field, by name, such as "slug" for "/slug". */
  fieldError(field: string): string | undefined {
    return this.problem.errors?.find((e) => e.pointer === `/${field}`)?.detail;
  }
}

function isProblem(value: unknown): value is Problem {
  return typeof value === "object" && value !== null && "status" in value && "title" in value;
}

/**
 * Waits for an API call and returns its data, or throws an ApiError for an
 * error response. Network failures throw as they are.
 */
export async function unwrap<T>(call: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await call;
  if (response.ok) {
    return data as T;
  }
  const problem: Problem = isProblem(error)
    ? error
    : {
        type: "about:blank",
        title: response.statusText || "Error",
        status: response.status,
        detail: "The server sent an unexpected response.",
      };
  throw new ApiError(response.status, problem);
}

/** A sentence that explains an error to a person. */
export function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    return error.problem.detail ?? error.problem.title;
  }
  if (error instanceof TypeError) {
    return "Couldn't reach the Signalbox server. Check your connection and try again.";
  }
  return "Something went wrong. Try again.";
}
