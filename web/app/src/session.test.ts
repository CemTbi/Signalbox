// SPDX-License-Identifier: Apache-2.0

import { createApiClient } from "@signalbox/api-client";
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { installSessionMiddleware } from "./api";
import { safeRedirect, sessionQuery } from "./session";

describe("safeRedirect", () => {
  it.each([
    ["/workspaces", "/workspaces"],
    ["/projects/prj_1?tab=docs#top", "/projects/prj_1?tab=docs#top"],
    ["//evil.example/path", undefined],
    ["/\\evil.example", undefined],
    ["https://evil.example", undefined],
    ["javascript:alert(1)", undefined],
    [42, undefined],
  ])("%s → %s", (input, want) => {
    expect(safeRedirect(input)).toBe(want);
  });
});

describe("sessionQuery", () => {
  it("is null when nobody is signed in", async () => {
    const fetch = vi.fn(async () =>
      Response.json({ type: "about:blank", title: "Unauthorized", status: 401 }, { status: 401 }),
    );
    const client = createApiClient({ baseUrl: "http://signalbox.test", fetch });
    const queryClient = new QueryClient();
    await expect(queryClient.fetchQuery(sessionQuery(client))).resolves.toBeNull();
  });

  it("keeps the CSRF token of the current session for unsafe requests", async () => {
    const session = {
      user: { id: "usr_01j9z3kq4m8r2t6v0x5w7y9a1b", email: "ada@example.com", name: "Ada" },
      expiresAt: "2026-10-23T12:00:00Z",
      csrfToken: "csrf-from-session",
    };
    const fetch = vi.fn(async (request: Request) =>
      request.url.endsWith("/sessions/current") ? Response.json(session) : new Response(null, { status: 204 }),
    );
    const client = createApiClient({ baseUrl: "http://signalbox.test", fetch });
    installSessionMiddleware(client);

    await new QueryClient().fetchQuery(sessionQuery(client));
    await client.DELETE("/api/v1/sessions/current");

    expect(fetch.mock.calls[1]![0].headers.get("X-CSRF-Token")).toBe("csrf-from-session");
  });
});
