// SPDX-License-Identifier: Apache-2.0

import type { Environment } from "@signalbox/api-client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { toPut, VariablesEditor } from "./VariablesEditor";

const staging: Environment = {
  id: "env_1",
  projectId: "prj_1",
  name: "Staging",
  slug: "staging",
  kind: "staging",
  position: 1,
  createdAt: "2026-10-18T12:00:00Z",
  updatedAt: "2026-10-18T12:00:00Z",
};

function ok(data: unknown, etag: string) {
  return Promise.resolve({ data, response: new Response(null, { headers: { ETag: etag } }) });
}

function renderEditor(canEdit = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <VariablesEditor environment={staging} canEdit={canEdit} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("toPut", () => {
  it("keeps stored secrets by leaving their value out", () => {
    const rows = [
      { id: 1, key: " baseUrl ", value: "https://a.test", secret: false, stored: false },
      { id: 2, key: "token", value: "", secret: true, stored: true },
      { id: 3, key: "newSecret", value: "s3cret", secret: true, stored: false },
      { id: 4, key: "", value: "orphan", secret: false, stored: false },
    ];
    expect(toPut(rows)).toEqual([
      { key: "baseUrl", value: "https://a.test", secret: false },
      { key: "token", secret: true },
      { key: "newSecret", value: "s3cret", secret: true },
    ]);
  });
});

describe("VariablesEditor", () => {
  it("shows variables, hides secrets, and saves with the version it loaded", async () => {
    vi.spyOn(api, "GET").mockImplementation(
      (() =>
        ok(
          {
            environmentId: "env_1",
            variables: [
              { key: "baseUrl", value: "https://staging.example.com", secret: false },
              { key: "token", secret: true },
            ],
          },
          '"3"',
        )) as never,
    );
    const put = vi.spyOn(api, "PUT").mockImplementation((() => ok({ environmentId: "env_1", variables: [] }, '"4"')) as never);
    renderEditor();

    expect(await screen.findByLabelText("Variable 1 value")).toHaveValue("https://staging.example.com");
    const secret = screen.getByLabelText("Variable 2 value");
    expect(secret).toHaveValue("");
    expect(secret).toHaveAttribute("type", "password");
    expect(secret).toHaveAttribute("placeholder", "Stored. Type to replace it.");

    await userEvent.type(screen.getByLabelText("Variable 3 name"), "orderId");
    await userEvent.type(screen.getByLabelText("Variable 3 value"), "42");
    await userEvent.click(screen.getByRole("button", { name: "Save variables" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
    const [, init] = put.mock.calls[0] as unknown as [string, { params: { header: Record<string, string> }; body: unknown }];
    expect(init.params.header["If-Match"]).toBe('"3"');
    expect(init.body).toEqual({
      variables: [
        { key: "baseUrl", value: "https://staging.example.com", secret: false },
        { key: "token", secret: true },
        { key: "orderId", value: "42", secret: false },
      ],
    });
  });

  it("is read-only for viewers", async () => {
    vi.spyOn(api, "GET").mockImplementation((() => ok({ environmentId: "env_1", variables: [{ key: "a", value: "1", secret: false }] }, '"1"')) as never);
    renderEditor(false);
    expect(await screen.findByLabelText("Variable 1 name")).toHaveAttribute("readonly");
    expect(screen.queryByRole("button", { name: "Save variables" })).not.toBeInTheDocument();
  });
});
