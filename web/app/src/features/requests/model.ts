// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry, HttpHeader, Item, RequestAuth, SavedRequest, SendRequest } from "@signalbox/api-client";

export const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

/** A header row in the editor. Unticked rows aren't sent. */
export interface HeaderRow {
  id: number;
  key: string;
  value: string;
  enabled: boolean;
}

/**
 * The auth being edited. It keeps every type's fields, so switching types
 * and back doesn't lose what was typed.
 */
export interface AuthDraft {
  type: RequestAuth["type"];
  username: string;
  password: string;
  token: string;
  key: string;
  value: string;
  in: "header" | "query";
}

/** What the editor holds. */
export interface Draft {
  method: string;
  url: string;
  headers: HeaderRow[];
  body: string;
  auth: AuthDraft;
}

export function noAuth(): AuthDraft {
  return { type: "none", username: "", password: "", token: "", key: "", value: "", in: "header" };
}

/** The auth to send or save: the chosen type's fields, or none. */
export function toAuth(a: AuthDraft): RequestAuth | undefined {
  switch (a.type) {
    case "basic":
      return { type: "basic", username: a.username, password: a.password };
    case "bearer":
      return { type: "bearer", token: a.token };
    case "apikey":
      return { type: "apikey", key: a.key.trim(), value: a.value, in: a.in };
    default:
      return undefined;
  }
}

function fromAuth(a: RequestAuth | undefined): AuthDraft {
  return { ...noAuth(), ...a, type: a?.type ?? "none", in: a?.in ?? "header" };
}

let nextRow = 1;

export function row(key = "", value = "", enabled = true): HeaderRow {
  return { id: nextRow++, key, value, enabled };
}

export function emptyDraft(): Draft {
  return { method: "GET", url: "", headers: [row()], body: "", auth: noAuth() };
}

/** The request to send: ticked headers with a name, and a body for methods that take one. */
export function toSend(d: Draft): SendRequest {
  const headers: HttpHeader[] = d.headers.filter((h) => h.enabled && h.key.trim() !== "").map((h) => ({ key: h.key.trim(), value: h.value }));
  const out: SendRequest = { method: d.method, url: d.url.trim(), headers };
  if (d.body !== "" && d.method !== "GET" && d.method !== "HEAD") out.body = d.body;
  const auth = toAuth(d.auth);
  if (auth) out.auth = auth;
  return out;
}

/**
 * A draft from a history entry. Credentials history kept as ••• come back
 * empty, so they're typed again rather than sent as dots. Headers auth
 * added aren't kept, since auth adds them again.
 */
export function fromEntry(e: HistoryEntry): Draft {
  const auth = fromAuth(e.request.auth);
  const added = auth.type === "basic" || auth.type === "bearer" ? "authorization" : auth.type === "apikey" && auth.in === "header" ? auth.key.toLowerCase() : "";
  const headers = e.request.headers.filter((h) => h.key.toLowerCase() !== added).map((h) => row(h.key, h.value === "•••" ? "" : h.value));
  const url = auth.type === "apikey" && auth.in === "query" ? withoutParam(e.url, auth.key) : e.url;
  return { method: e.method, url, headers: [...headers, row()], body: e.request.body ?? "", auth };
}

/**
 * The URL without the query parameter an API key added, which auth adds
 * again on send. Only the last one goes: auth appended it.
 */
export function withoutParam(url: string, name: string): string {
  const hash = url.indexOf("#");
  const [base, fragment] = hash < 0 ? [url, ""] : [url.slice(0, hash), url.slice(hash)];
  const q = base.indexOf("?");
  if (q < 0 || name === "") return url;
  const params = base.slice(q + 1).split("&");
  const decode = (s: string) => {
    try {
      return decodeURIComponent(s.replace(/\+/g, " "));
    } catch {
      return s;
    }
  };
  const at = params.findLastIndex((p) => decode(p.split("=")[0] ?? "") === name);
  if (at < 0) return url;
  params.splice(at, 1);
  const query = params.join("&");
  return base.slice(0, q) + (query ? `?${query}` : "") + fragment;
}

/** Whether a history entry had credentials or secrets history didn't keep. */
export function hadCredentials(e: HistoryEntry): boolean {
  return (
    (e.request.auth !== undefined && e.request.auth.type !== "none") ||
    e.request.headers.some((h) => h.value.includes("•••")) ||
    e.url.includes("•••") ||
    (e.request.body ?? "").includes("•••")
  );
}

/** The response's content type, without parameters. */
export function contentType(headers: HttpHeader[]): string {
  const h = headers.find((x) => x.key.toLowerCase() === "content-type");
  return (h?.value ?? "").split(";")[0]?.trim().toLowerCase() ?? "";
}

/** A body for reading: JSON indented, anything else as it came. */
export function prettyBody(body: string, type: string): string {
  if (type === "application/json" || type.endsWith("+json")) {
    try {
      return JSON.stringify(JSON.parse(body), null, 2);
    } catch {
      return body;
    }
  }
  return body;
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function formatMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms >= 10) return `${Math.round(ms)} ms`;
  return `${ms.toFixed(1)} ms`;
}

/** The phases of a request in the order they happen, for the timings view. */
export function phases(t: HistoryEntry["timings"]) {
  return [
    { label: "DNS lookup", ms: t.dnsMs },
    { label: "Connect", ms: t.connectMs },
    { label: "TLS handshake", ms: t.tlsMs },
    { label: "Waiting for the first byte", ms: t.firstByteMs },
    { label: "Download", ms: t.downloadMs },
  ];
}

/** A draft from a saved request. */
export function fromSaved(r: SavedRequest): Draft {
  const headers = (r.headers ?? []).map((h) => row(h.key, h.value, h.enabled));
  return { method: r.method, url: r.url, headers: [...headers, row()], body: r.body ?? "", auth: fromAuth(r.auth) };
}

/** A draft as a collection keeps it: every header with a name, ticked or not. */
export function toSaved(d: Draft): SavedRequest {
  return {
    method: d.method,
    url: d.url,
    headers: d.headers.filter((h) => h.key.trim() !== "").map((h) => ({ key: h.key.trim(), value: h.value, enabled: h.enabled })),
    body: d.body,
    ...(toAuth(d.auth) ? { auth: toAuth(d.auth) } : {}),
  };
}

/** A collection's items as a tree: each folder's children in order. */
export function tree(items: Item[]): Map<string, Item[]> {
  const children = new Map<string, Item[]>();
  for (const it of items) {
    const parent = it.parentId ?? "";
    const list = children.get(parent) ?? [];
    list.push(it);
    children.set(parent, list);
  }
  return children;
}

/** Folders in tree order, with their depth, for choosing where to save. */
export function folders(items: Item[]): { item: Item; depth: number }[] {
  const children = tree(items);
  const out: { item: Item; depth: number }[] = [];
  const walk = (parent: string, depth: number) => {
    for (const it of children.get(parent) ?? []) {
      if (it.kind !== "folder") continue;
      out.push({ item: it, depth });
      walk(it.id, depth + 1);
    }
  };
  walk("", 0);
  return out;
}
