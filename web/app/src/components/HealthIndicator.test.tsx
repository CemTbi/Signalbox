// SPDX-License-Identifier: Apache-2.0

import { createApiClient } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { HealthIndicator } from "./HealthIndicator";

function client(readyStatus: number, readyBody: unknown) {
  const fetch = vi.fn(async (request: Request) => {
    if (new URL(request.url).pathname === "/readyz") {
      return Response.json(readyBody, { status: readyStatus });
    }
    return Response.json({ product: "Signalbox", version: "0.1.0", apiVersion: "v1", roles: ["api"] });
  });
  return createApiClient({ baseUrl: "http://signalbox.test", fetch });
}

describe("HealthIndicator", () => {
  it("shows a healthy server and its version", async () => {
    render(<HealthIndicator client={client(200, { status: "pass" })} />);
    expect(await screen.findByText("Healthy")).toBeInTheDocument();
    expect(screen.getByText("v0.1.0")).toBeInTheDocument();
  });

  it("shows a server that is shutting down as unavailable", async () => {
    render(<HealthIndicator client={client(503, { status: "fail", output: "shutting down" })} />);
    expect(await screen.findByText("Unavailable")).toBeInTheDocument();
  });

  it("shows when the server can't be reached", async () => {
    const fetch = vi.fn(async () => {
      throw new TypeError("fetch failed");
    });
    render(<HealthIndicator client={createApiClient({ baseUrl: "http://signalbox.test", fetch })} />);
    expect(await screen.findByText("Server unreachable")).toBeInTheDocument();
  });
});
