// SPDX-License-Identifier: Apache-2.0

import type { ReferenceOperation, ReferenceSecurityScheme } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "./Markdown";
import { Operation, type Target } from "./Operation";
import { requestURL, sample, sampleRequest } from "./samples";
import { typeLabel } from "./SchemaView";

const createOrder: ReferenceOperation = {
  id: "createOrder",
  operationId: "createOrder",
  method: "POST",
  path: "/shops/{shop}/orders",
  summary: "Create an order",
  parameters: [
    { name: "shop", in: "path", required: true, schema: { types: ["string"] }, example: "berlin" },
    { name: "dryRun", in: "query", schema: { types: ["boolean"] } },
    { name: "Idempotency-Key", in: "header", required: true, schema: { types: ["string"], format: "uuid" } },
  ],
  requestBody: {
    required: true,
    content: [
      {
        type: "application/json",
        schema: {
          types: ["object"],
          properties: [
            { name: "sku", required: true, schema: { types: ["string"], constraints: ["at most 32 characters"] } },
            { name: "lines", schema: { types: ["array"], items: { name: "Line", ref: true } } },
          ],
        },
        examples: [{ value: `{"sku": "it's"}` }],
      },
    ],
  },
  responses: [
    { code: "201", description: "Created." },
    { code: "400", description: "Invalid." },
  ],
  security: [[{ scheme: "bearer" }]],
};

const schemes: ReferenceSecurityScheme[] = [{ name: "bearer", type: "http", scheme: "bearer" }];

describe("samples", () => {
  it("fill in path parameters and add required query parameters", () => {
    expect(requestURL("https://api.example.com/v1/", "/a/{id}/b", { path: { id: "x y" }, query: { q: "1", empty: "" }, header: {} })).toBe(
      "https://api.example.com/v1/a/x%20y/b?q=1",
    );
  });

  it("send required headers, credentials and the first example body", () => {
    const r = sampleRequest(createOrder, "https://mock.test", schemes);
    expect(r.url).toBe("https://mock.test/shops/berlin/orders");
    expect(r.headers).toEqual([
      ["Content-Type", "application/json"],
      ["Idempotency-Key", "00000000-0000-0000-0000-000000000000"],
      ["Authorization", "Bearer $TOKEN"],
    ]);
    expect(r.body?.text).toBe(`{"sku": "it's"}`);
  });

  it("quote bodies for the shell and as string literals", () => {
    const r = sampleRequest(createOrder, "https://mock.test", schemes);
    expect(sample("curl", r)).toContain(`--data '{"sku": "it'"'"'s"}'`);
    expect(sample("javascript", r)).toContain(`body: "{\\"sku\\": \\"it's\\"}"`);
    expect(sample("python", r)).toContain("requests.request(");
    expect(sample("go", r)).toContain(`http.NewRequest("POST", "https://mock.test/shops/berlin/orders"`);
    expect(sample("java", r)).toContain(`.header("Authorization", "Bearer $TOKEN")`);
    // C# sets the content type on the content, not the request.
    const cs = sample("csharp", r);
    expect(cs).not.toContain(`TryAddWithoutValidation("Content-Type"`);
    expect(cs).toContain(`new StringContent(`);
  });
});

describe("typeLabel", () => {
  it("names arrays, formats, references and null", () => {
    expect(typeLabel({ types: ["array"], items: { name: "Order", ref: true } })).toBe("array of Order");
    expect(typeLabel({ types: ["string"], format: "date-time", nullable: true })).toBe("string (date-time) or null");
    expect(typeLabel({ oneOf: [{ types: ["string"] }] })).toBe("one of");
  });
});

describe("Markdown", () => {
  it("renders CommonMark and tables but not HTML or script links", () => {
    const { container } = render(
      <Markdown source={"**Bold** [ok](https://example.com) [bad](javascript:alert(1)) <img src=x onerror=alert(1)>\n\n| a |\n|---|\n| b |"} />,
    );
    expect(container.querySelector("strong")).toHaveTextContent("Bold");
    expect(container.querySelector('a[href="https://example.com"]')).not.toBeNull();
    expect(container.querySelector('a[href^="javascript"]')).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("table")).not.toBeNull();
  });
});

describe("Operation", () => {
  const target: Target = { base: "https://mock.test", mock: "https://mock.test", schemes, language: "curl", onLanguage: vi.fn() };

  afterEach(() => vi.unstubAllGlobals());

  it("shows parameters, the body's schema, responses and a sample", () => {
    render(<Operation op={createOrder} anchor="op-0-createOrder" target={target} />);
    expect(screen.getByRole("heading", { name: "Create an order" })).toBeInTheDocument();
    expect(screen.getByText("Idempotency-Key")).toBeInTheDocument();
    expect(screen.getByText("at most 32 characters")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "array of Line" })).toHaveAttribute("href", "#schema-Line");
    expect(screen.getByText("Authorize with bearer.", { exact: false })).toBeInTheDocument();
    expect(screen.getByText(/curl -X POST/)).toBeInTheDocument();
  });

  it("tries the operation against the mock", async () => {
    const fetchMock = vi.fn(
      async () => new Response('{"id":1}', { status: 201, statusText: "Created", headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(<Operation op={createOrder} anchor="op-0-createOrder" target={target} />);
    await userEvent.click(screen.getByRole("button", { name: "Try it against the mock" }));
    await userEvent.type(screen.getByLabelText(/Authorization/), "Bearer t");
    await userEvent.selectOptions(screen.getByLabelText("Response"), "400");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByText(/"id": 1/)).toBeInTheDocument();
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("https://mock.test/shops/berlin/orders");
    const headers = new Headers(init.headers);
    expect(init.method).toBe("POST");
    expect(headers.get("Authorization")).toBe("Bearer t");
    expect(headers.get("Prefer")).toBe("code=400");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(init.body).toBe(`{"sku": "it's"}`);
  });
});
