// SPDX-License-Identifier: Apache-2.0

import { type FormEvent, type KeyboardEvent, useId, useState } from "react";
import { type Draft, type HeaderRow, methods, row } from "./model";

/**
 * The request being written: method, URL, headers and body. Ctrl+Enter
 * (⌘+Enter on a Mac) sends it from anywhere in the form.
 */
export function RequestEditor({
  draft,
  onChange,
  onSend,
  sending,
  canSend,
}: {
  draft: Draft;
  onChange: (d: Draft) => void;
  onSend: () => void;
  sending: boolean;
  canSend: boolean;
}) {
  const [tab, setTab] = useState<"headers" | "body">("headers");
  const ids = { method: useId(), url: useId(), body: useId() };
  const headerCount = draft.headers.filter((h) => h.enabled && h.key.trim() !== "").length;

  function submit(e: FormEvent) {
    e.preventDefault();
    if (canSend && !sending) onSend();
  }
  function shortcut(e: KeyboardEvent) {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if (canSend && !sending) onSend();
    }
  }
  function setHeader(id: number, change: Partial<HeaderRow>) {
    const headers = draft.headers.map((h) => (h.id === id ? { ...h, ...change } : h));
    // A row is always free for the next header.
    if (headers.at(-1)?.key || headers.at(-1)?.value) headers.push(row());
    onChange({ ...draft, headers });
  }

  return (
    <form className="request-editor" onSubmit={submit} onKeyDown={shortcut} aria-label="Request">
      <div className="request-line">
        <label htmlFor={ids.method} className="visually-hidden">
          Method
        </label>
        <select id={ids.method} className="mono" value={draft.method} onChange={(e) => onChange({ ...draft, method: e.target.value })}>
          {methods.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <label htmlFor={ids.url} className="visually-hidden">
          URL
        </label>
        <input
          id={ids.url}
          className="mono"
          type="text"
          inputMode="url"
          placeholder="https://api.example.com/orders or {{baseUrl}}/orders"
          value={draft.url}
          onChange={(e) => onChange({ ...draft, url: e.target.value })}
          spellCheck={false}
          autoComplete="off"
        />
        <button type="submit" className="button primary" disabled={!canSend || sending || draft.url.trim() === ""}>
          {sending ? "Sending…" : "Send"}
        </button>
      </div>
      <div className="tabs" role="tablist" aria-label="Request parts">
        <button type="button" role="tab" aria-selected={tab === "headers"} onClick={() => setTab("headers")}>
          Headers{headerCount > 0 ? ` (${headerCount})` : ""}
        </button>
        <button type="button" role="tab" aria-selected={tab === "body"} onClick={() => setTab("body")}>
          Body{draft.body ? " •" : ""}
        </button>
      </div>
      {tab === "headers" ? (
        <table className="header-rows" role="tabpanel">
          <thead>
            <tr>
              <th scope="col">
                <span className="visually-hidden">Send</span>
              </th>
              <th scope="col">Name</th>
              <th scope="col">Value</th>
              <th scope="col">
                <span className="visually-hidden">Remove</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {draft.headers.map((h, i) => (
              <tr key={h.id}>
                <td>
                  <input type="checkbox" checked={h.enabled} onChange={(e) => setHeader(h.id, { enabled: e.target.checked })} aria-label={`Send header ${i + 1}`} />
                </td>
                <td>
                  <input className="mono" value={h.key} placeholder="Name" aria-label={`Header ${i + 1} name`} onChange={(e) => setHeader(h.id, { key: e.target.value })} />
                </td>
                <td>
                  <input className="mono" value={h.value} placeholder="Value" aria-label={`Header ${i + 1} value`} onChange={(e) => setHeader(h.id, { value: e.target.value })} />
                </td>
                <td>
                  {i < draft.headers.length - 1 ? (
                    <button type="button" className="button ghost small" aria-label={`Remove header ${h.key || i + 1}`} onClick={() => onChange({ ...draft, headers: draft.headers.filter((x) => x.id !== h.id) })}>
                      Remove
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <div className="field" role="tabpanel">
          <label htmlFor={ids.body}>Body</label>
          <textarea id={ids.body} className="mono" rows={10} value={draft.body} onChange={(e) => onChange({ ...draft, body: e.target.value })} spellCheck={false} />
          <p className="field-hint">
            {draft.method === "GET" || draft.method === "HEAD"
              ? `${draft.method} requests are sent without a body.`
              : "Sent as written. Set Content-Type under Headers, such as application/json."}
          </p>
        </div>
      )}
    </form>
  );
}
