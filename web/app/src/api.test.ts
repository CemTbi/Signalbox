// SPDX-License-Identifier: Apache-2.0

import { createApiClient } from "@signalbox/api-client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, describeError, installSessionMiddleware, onSessionEnded, setCsrfToken, unwrap } from "./api";

function client(response: () => Response) {
  const fetch = vi.fn(async (_request: Request) => response());
  const c = createApiClient({ baseUrl: "http://signalbox.test", fetch });
  installSessionMiddleware(c);
  return { fetch, client: c };
}

afterEach(() => setCsrfToken(undefined));

describe("session middleware", () => {
  it("sends the CSRF token on unsafe requests only", async () => {
    const { fetch, client: c } = client(() => Response.json({ data: [] }));
    setCsrfToken("token-123");

    await c.GET("/api/v1/workspaces");
    await c.POST("/api/v1/workspaces", { body: { name: "Payments", slug: "payments" } });

    expect(fetch.mock.calls[0]![0].headers.get("X-CSRF-Token")).toBeNull();
    expect(fetch.mock.calls[1]![0].headers.get("X-CSRF-Token")).toBe("token-123");
  });

  it("reports an ended session, but not a failed sign-in", async () => {
    const { client: c } = client(() =>
      Response.json({ type: "about:blank", title: "Unauthorized", status: 401 }, { status: 401 }),
    );
    const ended = vi.fn();
    const stop = onSessionEnded(ended);

    await c.POST("/api/v1/sessions", { body: { email: "a@example.com", password: "x" } });
    await c.GET("/api/v1/sessions/current");
    expect(ended).not.toHaveBeenCalled();

    await c.GET("/api/v1/workspaces");
    expect(ended).toHaveBeenCalledOnce();
    stop();
  });
});

describe("unwrap", () => {
  it("returns the data of a successful response", async () => {
    const { client: c } = client(() => Response.json({ required: true }));
    await expect(unwrap(c.GET("/api/v1/setup"))).resolves.toEqual({ required: true });
  });

  it("throws the problem of an error response, with its field errors", async () => {
    const { client: c } = client(() =>
      Response.json(
        {
          type: "about:blank",
          title: "Bad Request",
          status: 400,
          code: "validation-failed",
          detail: "The request has 1 invalid field.",
          errors: [{ pointer: "/slug", detail: "Use lowercase letters.", code: "pattern" }],
        },
        { status: 400, headers: { "Content-Type": "application/problem+json" } },
      ),
    );
    const error = await unwrap(c.POST("/api/v1/workspaces", { body: { name: "X", slug: "Bad" } })).catch((e) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error.code).toBe("validation-failed");
    expect(error.fieldError("slug")).toBe("Use lowercase letters.");
    expect(error.fieldError("name")).toBeUndefined();
  });

  it("explains a response that isn't a problem document", async () => {
    const { client: c } = client(() => new Response("<html>Bad gateway</html>", { status: 502 }));
    const error = await unwrap(c.GET("/api/v1/setup")).catch((e) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(describeError(error)).toBe("The server sent an unexpected response.");
  });
});

describe("describeError", () => {
  it("explains network failures", () => {
    expect(describeError(new TypeError("Failed to fetch"))).toMatch(/Couldn't reach the Signalbox server/);
  });
});
