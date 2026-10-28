// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry } from "@signalbox/api-client";
import { useState } from "react";
import { contentType, formatMs, formatSize, phases, prettyBody } from "./model";

/** What came of a request: the response, or why there was none. */
export function ResponseView({ entry }: { entry: HistoryEntry }) {
  const scripts = entry.scripts;
  const [tab, setTab] = useState<"body" | "headers" | "timings" | "tests" | "console">(scripts?.tests.length ? "tests" : "body");
  const r = entry.response;
  const t = entry.timings;

  if (!r) {
    return (
      <section className="response" aria-label="Response">
        <p className="response-failure" role="alert">
          <strong>{entry.failure?.code === "skipped" ? "Not sent." : "No response."}</strong> {entry.failure?.message}
        </p>
        {t.totalMs > 0 ? <p className="field-hint">Gave up after {formatMs(t.totalMs)}.</p> : null}
        {scripts ? <ScriptsView scripts={scripts} /> : null}
      </section>
    );
  }
  const passed = scripts?.tests.filter((x) => x.passed).length ?? 0;

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
        {scripts ? (
          <>
            <button type="button" role="tab" aria-selected={tab === "tests"} onClick={() => setTab("tests")}>
              Tests ({passed}/{scripts.tests.length})
            </button>
            <button type="button" role="tab" aria-selected={tab === "console"} onClick={() => setTab("console")}>
              Console ({scripts.logs.length})
            </button>
          </>
        ) : null}
      </div>
      {scripts?.errors.length ? <ScriptErrors errors={scripts.errors} /> : null}
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
      ) : tab === "timings" ? (
        <Timings timings={t} remote={r.remoteAddress} />
      ) : tab === "tests" && scripts ? (
        <TestList scripts={scripts} />
      ) : scripts ? (
        <ConsoleList scripts={scripts} />
      ) : null}
    </section>
  );
}

export type Outcome = NonNullable<HistoryEntry["scripts"]>;

/** What the scripts did when there was no response to show beside it. */
function ScriptsView({ scripts }: { scripts: Outcome }) {
  return (
    <>
      {scripts.errors.length ? <ScriptErrors errors={scripts.errors} /> : null}
      {scripts.tests.length ? <TestList scripts={scripts} /> : null}
      {scripts.logs.length ? <ConsoleList scripts={scripts} /> : null}
    </>
  );
}

export function ScriptErrors({ errors }: { errors: Outcome["errors"] }) {
  return (
    <ul className="script-errors" role="alert">
      {errors.map((e, i) => (
        <li key={i}>
          The {e.event === "prerequest" ? "pre-request" : "test"} script of <strong>{e.from}</strong> stopped: {e.name}: {e.message}
        </li>
      ))}
    </ul>
  );
}

export function TestList({ scripts }: { scripts: Outcome }) {
  if (scripts.tests.length === 0) return <p className="field-hint" role="tabpanel">The scripts ran no tests. Add some with pm.test.</p>;
  return (
    <div role="tabpanel">
      <ul className="test-list" aria-label="Tests">
        {scripts.tests.map((x, i) => (
          <li key={i} className={x.skipped ? "test-skipped" : x.passed ? "test-passed" : "test-failed"}>
            <span className="test-mark">{x.skipped ? "Skipped" : x.passed ? "Passed" : "Failed"}</span>
            <span>{x.name}</span>
            <span className="muted"> · {x.from}</span>
            {x.error ? <span className="test-error mono">{x.error}</span> : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ConsoleList({ scripts }: { scripts: Outcome }) {
  return (
    <div role="tabpanel">
      {scripts.logs.length === 0 ? (
        <p className="field-hint">The scripts logged nothing. Use console.log to see values here.</p>
      ) : (
        <ol className="console-list mono" aria-label="Console">
          {scripts.logs.map((l, i) => (
            <li key={i} className={`console-${l.level}`}>
              <span className="muted">{l.from}</span> {l.message}
            </li>
          ))}
        </ol>
      )}
      {scripts.changes.length ? (
        <p className="field-hint">
          Scripts changed {[...new Set(scripts.changes.map((c) => (c.key ? `${c.key} (${c.scope})` : `${c.scope} (cleared)`)))].join(", ")} for this send only;
          the changes aren't saved.
        </p>
      ) : null}
    </div>
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
