// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry } from "@signalbox/api-client";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { type Draft, emptyDraft, folders, formatMs, formatSize, fromEntry, fromSaved, noAuth, prettyBody, row, toSaved, toSend, tree, withoutParam } from "./model";
import { RequestEditor } from "./RequestEditor";
import { ResponseView } from "./ResponseView";

const entry: HistoryEntry = {
  id: "hist_1",
  method: "POST",
  url: "https://api.example.com/orders",
  sentAt: "2026-10-18T12:00:00Z",
  request: { headers: [{ key: "Authorization", value: "•••" }, { key: "Accept", value: "application/json" }], body: '{"sku":"A-1"}' },
  response: {
    status: 201,
    statusText: "Created",
    proto: "HTTP/1.1",
    headers: [{ key: "Content-Type", value: "application/json; charset=utf-8" }],
    body: '{"id":42,"lines":[]}',
    bodyEncoding: "text",
    size: 20,
    truncated: false,
    url: "https://api.example.com/orders",
    redirects: 0,
    remoteAddress: "203.0.113.7:443",
  },
  timings: { dnsMs: 4, connectMs: 12, tlsMs: 25, firstByteMs: 40, downloadMs: 0.5, totalMs: 82 },
};

describe("model", () => {
  it("sends ticked headers with names, and no body for GET", () => {
    const d: Draft = { method: "GET", url: " https://a.test ", body: "ignored", headers: [row("Accept", "*/*"), row("X-Off", "1", false), row("", "orphan")], auth: noAuth() };
    expect(toSend(d)).toEqual({ method: "GET", url: "https://a.test", headers: [{ key: "Accept", value: "*/*" }] });
    expect(toSend({ ...d, method: "POST" }).body).toBe("ignored");
  });

  it("reopens history without the credentials it didn't keep", () => {
    const d = fromEntry(entry);
    expect(d.headers.map((h) => [h.key, h.value])).toEqual([
      ["Authorization", ""],
      ["Accept", "application/json"],
      ["", ""],
    ]);
    expect(d.body).toBe('{"sku":"A-1"}');
  });

  it("reopens history with its auth type, without the header auth added", () => {
    const d = fromEntry({ ...entry, request: { ...entry.request, auth: { type: "bearer" } } });
    expect(d.auth.type).toBe("bearer");
    expect(d.auth.token).toBe("");
    expect(d.headers.map((h) => h.key)).toEqual(["Accept", ""]);
    expect(toSaved({ ...d, auth: noAuth() }).auth).toBeUndefined();
  });

  it("reopens history without the query parameter an API key added", () => {
    const d = fromEntry({ ...entry, url: "{{baseUrl}}/orders?page=2&api_key=•••", request: { ...entry.request, auth: { type: "apikey", key: "api_key", in: "query" } } });
    expect(d.url).toBe("{{baseUrl}}/orders?page=2");
    expect(withoutParam("https://a.test/x?api%20key=1#top", "api key")).toBe("https://a.test/x#top");
    expect(withoutParam("https://a.test/x?a=1&k=2&k=3", "k")).toBe("https://a.test/x?a=1&k=2");
    expect(withoutParam("https://a.test/x", "k")).toBe("https://a.test/x");
  });

  it("formats bodies, sizes and times", () => {
    expect(prettyBody('{"a":1}', "application/problem+json")).toBe('{\n  "a": 1\n}');
    expect(prettyBody("{not json", "application/json")).toBe("{not json");
    expect(formatSize(2048)).toBe("2.0 KB");
    expect(formatMs(1234)).toBe("1.23 s");
    expect(formatMs(3.25)).toBe("3.3 ms");
  });
});

describe("ResponseView", () => {
  it("shows the status, a readable body, headers and timings", async () => {
    render(<ResponseView entry={entry} />);
    expect(screen.getByText("201")).toBeInTheDocument();
    expect(screen.getByText(/"lines": \[\]/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Headers (1)" }));
    expect(screen.getByText("application/json; charset=utf-8")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Timings" }));
    expect(screen.getByText("TLS handshake")).toBeInTheDocument();
    expect(screen.getByText(/from 203\.0\.113\.7:443/)).toBeInTheDocument();
  });

  it("says why there was no response", () => {
    render(<ResponseView entry={{ ...entry, response: undefined, failure: { code: "blocked", message: "Signalbox doesn't send requests to 169.254.169.254." } }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("No response. Signalbox doesn't send requests to 169.254.169.254.");
  });

  it("doesn't show binary bodies", () => {
    render(<ResponseView entry={{ ...entry, response: { ...entry.response!, body: "iVBORw0KGgo=", bodyEncoding: "base64" } }} />);
    expect(screen.getByText(/The body is binary/)).toBeInTheDocument();
  });
});

describe("RequestEditor", () => {
  function Editor({ onSend, canSend = true }: { onSend: (d: Draft) => void; canSend?: boolean }) {
    const [draft, setDraft] = useState<Draft>(emptyDraft);
    return <RequestEditor draft={draft} onChange={setDraft} onSend={() => onSend(draft)} sending={false} canSend={canSend} />;
  }

  it("builds a request and sends it with the button or Ctrl+Enter", async () => {
    const onSend = vi.fn();
    render(<Editor onSend={onSend} />);
    await userEvent.selectOptions(screen.getByLabelText("Method"), "POST");
    await userEvent.type(screen.getByLabelText("URL"), "https://api.example.com/orders");
    await userEvent.type(screen.getByLabelText("Header 1 name"), "Content-Type");
    await userEvent.type(screen.getByLabelText("Header 1 value"), "application/json");
    // A new empty row appears for the next header.
    expect(screen.getByLabelText("Header 2 name")).toHaveValue("");
    await userEvent.click(screen.getByRole("tab", { name: "Body" }));
    await userEvent.type(screen.getByLabelText("Body"), "{{}");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(toSend(onSend.mock.calls[0]![0])).toEqual({
      method: "POST",
      url: "https://api.example.com/orders",
      headers: [{ key: "Content-Type", value: "application/json" }],
      body: "{}",
    });
    await userEvent.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).toHaveBeenCalledTimes(2);
  });

  it("sends auth from the Auth tab, keeping fields across type changes", async () => {
    const onSend = vi.fn();
    render(<Editor onSend={onSend} />);
    await userEvent.type(screen.getByLabelText("URL"), "https://api.example.com/me");
    await userEvent.click(screen.getByRole("tab", { name: "Auth" }));
    await userEvent.selectOptions(screen.getByLabelText("Type"), "bearer");
    await userEvent.type(screen.getByLabelText("Token"), "{{{{token}}");
    await userEvent.selectOptions(screen.getByLabelText("Type"), "apikey");
    await userEvent.type(screen.getByLabelText("Key"), "api_key");
    await userEvent.selectOptions(screen.getByLabelText("Send it in"), "query");
    await userEvent.selectOptions(screen.getByLabelText("Type"), "bearer");
    expect(screen.getByLabelText("Token")).toHaveValue("{{token}}");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(toSend(onSend.mock.calls[0]![0]).auth).toEqual({ type: "bearer", token: "{{token}}" });
    expect(screen.getByRole("tab", { name: "Auth •" })).toBeInTheDocument();
  });

  it("can't send for viewers", async () => {
    const onSend = vi.fn();
    render(<Editor onSend={onSend} canSend={false} />);
    await userEvent.type(screen.getByLabelText("URL"), "https://api.example.com/");
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });
});

describe("saved requests", () => {
  it("keep unticked headers, and come back as drafts", () => {
    const d: Draft = { method: "POST", url: "{{baseUrl}}/orders", body: "{}", headers: [row("Accept", "*/*"), row("X-Off", "1", false), row("", "")], auth: noAuth() };
    const saved = toSaved(d);
    expect(saved.headers).toEqual([
      { key: "Accept", value: "*/*", enabled: true },
      { key: "X-Off", value: "1", enabled: false },
    ]);
    const back = fromSaved(saved);
    expect(back.headers.map((h) => [h.key, h.enabled])).toEqual([
      ["Accept", true],
      ["X-Off", false],
      ["", true],
    ]);
  });

  it("make a tree, and list folders in order with their depth", () => {
    const item = (id: string, kind: "folder" | "request", parentId?: string) =>
      ({ id, kind, name: id, collectionId: "col_1", createdAt: "", updatedAt: "", ...(parentId ? { parentId } : {}) }) as const;
    const items = [item("a", "folder"), item("r", "request"), item("b", "folder", "a"), item("c", "folder", "b"), item("d", "folder")];
    expect(tree(items).get("a")?.map((i) => i.id)).toEqual(["b"]);
    expect(folders(items).map(({ item, depth }) => `${item.id}:${depth}`)).toEqual(["a:0", "b:1", "c:2", "d:0"]);
  });
});
