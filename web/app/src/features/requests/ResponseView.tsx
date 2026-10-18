// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry } from "@signalbox/api-client";
import { useState } from "react";
import { contentType, formatMs, formatSize, phases, prettyBody } from "./model";

/** What came of a request: the response, or why there was none. */
export function ResponseView({ entry }: { entry: HistoryEntry }) {
  const [tab, setTab] = useState<"body" | "headers" | "timings">("body");
  const r = entry.response;
  const t = entry.timings;

  if (!r) {
    return (
      <section className="response" aria-label="Response">
        <p className="response-failure" role="alert">
          <strong>No response.</strong> {entry.failure?.message}
        </p>
        {t.totalMs > 0 ? <p className="field-hint">Gave up after {formatMs(t.totalMs)}.</p> : null}
      </section>
    );
  }

  const type = contentType(r.headers);
  const body = r.bodyEncoding === "base64" ? null : prettyBody(r.body, type);
  return (
    <section className="response" aria-label="Response">
      <p className="response-line">
        <span className={`status status-${String(r.status).charAt(0)}`}>{r.status}</span> {r.statusText}
        <span className="muted">
          {" "}
          · {formatMs(t.totalMs)} · {formatSize(r.size)}
          {r.truncated ? " (cut)" : ""}
          {r.redirects > 0 ? ` · ${r.redirects} redirect${r.redirects === 1 ? "" : "s"}` : ""}
        </span>
      </p>
      <div className="tabs" role="tablist" aria-label="Response parts">
        <button type="button" role="tab" aria-selected={tab === "body"} onClick={() => setTab("body")}>
          Body
        </button>
        <button type="button" role="tab" aria-selected={tab === "headers"} onClick={() => setTab("headers")}>
          Headers ({r.headers.length})
        </button>
        <button type="button" role="tab" aria-selected={tab === "timings"} onClick={() => setTab("timings")}>
          Timings
        </button>
      </div>
      {tab === "body" ? (
        <div role="tabpanel">
          {r.truncated ? <p className="field-hint">The body was longer than Signalbox keeps; this is its start.</p> : null}
          {body === null ? (
            <p className="field-hint">
              The body is binary ({formatSize(r.size)}{type ? `, ${type}` : ""}), so it isn't shown.
            </p>
          ) : body === "" ? (
            <p className="field-hint">The response has no body.</p>
          ) : (
            <pre className="code" tabIndex={0}>
              <code>{body}</code>
            </pre>
          )}
        </div>
      ) : tab === "headers" ? (
        <table className="kv" role="tabpanel">
          <tbody>
            {r.headers.map((h, i) => (
              <tr key={`${h.key}-${i}`}>
                <th scope="row" className="mono">
                  {h.key}
                </th>
                <td className="mono">{h.value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <Timings timings={t} remote={r.remoteAddress} />
      )}
    </section>
  );
}

/** Each phase as a bar along the request's whole time. */
function Timings({ timings, remote }: { timings: HistoryEntry["timings"]; remote?: string }) {
  let offset = 0;
  const total = Math.max(timings.totalMs, 0.001);
  return (
    <div role="tabpanel" className="timings">
      <ol>
        {phases(timings).map((p) => {
          const start = offset;
          offset += p.ms;
          return (
            <li key={p.label}>
              <span className="timing-label">{p.label}</span>
              <span className="timing-track" aria-hidden="true">
                <span className="timing-bar" style={{ left: `${(start / total) * 100}%`, width: `${Math.max((p.ms / total) * 100, p.ms > 0 ? 0.5 : 0)}%` }} />
              </span>
              <span className="timing-value">{p.ms > 0 ? formatMs(p.ms) : "—"}</span>
            </li>
          );
        })}
      </ol>
      <p className="field-hint">
        {formatMs(timings.totalMs)} in all{remote ? `, from ${remote}` : ""}. A dash means the phase didn't happen, such as DNS for an address or TLS
        on a reused connection.
      </p>
    </div>
  );
}
