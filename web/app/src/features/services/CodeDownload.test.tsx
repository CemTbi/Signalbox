// SPDX-License-Identifier: Apache-2.0

import { createApiClient, type Version } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CodeDownload } from "./CodeDownload";

const version = (id: string, name: string): Version => ({
  id,
  specId: "spec_01j9z3r4t6v8x0z2b4d6f8h0k2",
  name,
  revisionId: "rev_01j9z3s7v9x1z3b5d7f9h1k3m5",
  revisionNumber: 3,
  publishedBy: "usr_01j9z3kq4m8r2t6v0x5w7y9a1b",
  publishedAt: "2026-10-17T12:30:00Z",
});

describe("CodeDownload", () => {
  const created: string[] = [];
  beforeEach(() => {
    URL.createObjectURL = vi.fn(() => {
      created.push("blob:1");
      return "blob:1";
    });
    URL.revokeObjectURL = vi.fn();
  });
  afterEach(() => {
    created.length = 0;
  });

  it("downloads the chosen version and target, and reports warnings", async () => {
    const requests: string[] = [];
    const client = createApiClient({
      baseUrl: "https://signalbox.test",
      fetch: async (input: Request) => {
        requests.push(input.url);
        return new Response(new Blob(["zip"]), {
          status: 200,
          headers: {
            "Content-Type": "application/zip",
            "Content-Disposition": 'attachment; filename="orders-go-client-1.0.0.zip"',
            "X-Codegen-Warnings": "2",
          },
        });
      },
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    render(<CodeDownload client={client} versions={[version("ver_2", "1.1.0"), version("ver_1", "1.0.0")]} />);
    await userEvent.selectOptions(screen.getByLabelText("Version"), "ver_1");
    await userEvent.selectOptions(screen.getByLabelText("Generate"), "go-client");
    await userEvent.type(screen.getByLabelText("Package name (optional)"), "orders");
    await userEvent.click(screen.getByRole("button", { name: "Download zip" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Downloaded orders-go-client-1.0.0.zip. 2 parts of the spec");
    expect(requests).toEqual(["https://signalbox.test/api/v1/versions/ver_1/code?target=go-client&package=orders"]);
    expect(click).toHaveBeenCalledOnce();
    click.mockRestore();
  });

  it("explains a failure", async () => {
    const client = createApiClient({
      baseUrl: "https://signalbox.test",
      fetch: async () =>
        new Response(JSON.stringify({ type: "about:blank", title: "Bad Request", status: 400, code: "invalid-package", detail: "A package name is lowercase." }), {
          status: 400,
          headers: { "Content-Type": "application/problem+json" },
        }),
    });
    render(<CodeDownload client={client} versions={[version("ver_1", "1.0.0")]} />);
    await userEvent.click(screen.getByRole("button", { name: "Download zip" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("A package name is lowercase.");
    expect(created).toHaveLength(0);
  });

  it("shows nothing before a version is published", () => {
    const { container } = render(<CodeDownload client={createApiClient()} versions={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
