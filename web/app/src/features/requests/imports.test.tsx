// SPDX-License-Identifier: Apache-2.0

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api";
import { CurlImport } from "./CurlImport";
import { ImportCollection } from "./ImportCollection";
import { ImportReport, type Report } from "./ImportReport";

const report: Report = {
  clean: false,
  folders: 1,
  requests: 2,
  variables: 1,
  notes: [
    { level: "info", message: "1 saved example responses weren't imported.", item: "Orders / Get" },
    { level: "omitted", message: "The file field \"file\" wasn't imported.", item: "Upload" },
  ],
};

function ok(data: unknown) {
  return Promise.resolve({ data, response: new Response(null) });
}

function wrap(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ImportReport", () => {
  it("says whether it's clean, and lists what was left out first", () => {
    render(<ImportReport report={report} what="collection" />);
    expect(screen.getByText(/Some things will run differently/)).toHaveTextContent("1 folder, 2 requests, 1 variable.");
    const notes = screen.getAllByRole("listitem");
    expect(notes[0]).toHaveTextContent("Left out");
    expect(notes[0]).toHaveTextContent("Upload");
    expect(notes[1]).toHaveTextContent("Note");
  });

  it("says when it's clean", () => {
    render(<ImportReport report={{ ...report, clean: true, notes: [] }} what="collection" />);
    expect(screen.getByText(/Clean: nothing is left out/)).toBeInTheDocument();
    expect(screen.queryByRole("list")).not.toBeInTheDocument();
  });
});

describe("ImportCollection", () => {
  it("checks the file first, then imports it", async () => {
    const post = vi
      .spyOn(api, "POST")
      .mockImplementationOnce((() => ok({ report })) as never)
      .mockImplementationOnce((() => ok({ report, collection: { id: "col_1", name: "Shop API" } })) as never);
    wrap(<ImportCollection projectId="prj_1" onDone={() => {}} />);
    const file = new File([JSON.stringify({ info: { name: "Shop API" } })], "shop.json", { type: "application/json" });
    await userEvent.upload(screen.getByLabelText("Postman collection"), file);
    expect(await screen.findByText("Upload")).toBeInTheDocument();
    const first = post.mock.calls[0] as unknown as [string, { params: { query: { dryRun: boolean } } }];
    expect(first[1].params.query.dryRun).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Import shop.json" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Imported Shop API.");
    const second = post.mock.calls[1] as unknown as [string, { params: { query: { dryRun: boolean } } }];
    expect(second[1].params.query.dryRun).toBe(false);
  });

  it("says when the file isn't JSON", async () => {
    const post = vi.spyOn(api, "POST");
    wrap(<ImportCollection projectId="prj_1" onDone={() => {}} />);
    await userEvent.upload(screen.getByLabelText("Postman collection"), new File(["not json"], "x.json", { type: "application/json" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The file isn't JSON.");
    expect(post).not.toHaveBeenCalled();
  });
});

describe("CurlImport", () => {
  it("reads a command into a request", async () => {
    const request = { method: "POST", url: "https://a.test/orders", headers: [{ key: "accept", value: "*/*", enabled: true }] };
    vi.spyOn(api, "POST").mockImplementation((() => ok({ request, report: { ...report, notes: [] } })) as never);
    const onLoad = vi.fn();
    wrap(<CurlImport onLoad={onLoad} onCancel={() => {}} />);
    await userEvent.type(screen.getByLabelText("curl command"), "curl -X POST https://a.test/orders");
    await userEvent.click(screen.getByRole("button", { name: "Read command" }));
    await vi.waitFor(() => expect(onLoad).toHaveBeenCalledWith(request, expect.objectContaining({ requests: 2 })));
  });
});
