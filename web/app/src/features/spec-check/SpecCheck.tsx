// SPDX-License-Identifier: Apache-2.0

import type { ApiClient, Diagnostic, Report } from "@signalbox/api-client";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { api } from "../../api";
import { lineRange, summarize } from "./lines";
import { sampleSpec } from "./sample";

type CheckState =
  | { status: "idle" }
  | { status: "checking"; previous?: Report }
  | { status: "done"; report: Report }
  | { status: "failed"; message: string; previous?: Report };

const ruleSetOptions = [
  { value: "recommended", label: "Recommended" },
  { value: "recommended,owasp", label: "Recommended and OWASP security" },
  { value: "none", label: "Validation only" },
] as const;

const severityLabels: Record<Diagnostic["severity"], string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
  hint: "Hint",
};

/**
 * Validates and lints an OpenAPI document against the server's spec engine,
 * the same engine the CLI and CI use.
 */
export function SpecCheck({ client = api }: { client?: ApiClient }) {
  const [text, setText] = useState(sampleSpec);
  const [ruleSets, setRuleSets] = useState<string>(ruleSetOptions[0].value);
  const [state, setState] = useState<CheckState>({ status: "idle" });
  const editor = useRef<HTMLTextAreaElement>(null);
  const ids = { editor: useId(), ruleSets: useId(), results: useId() };

  const check = useCallback(
    async (document: string, sets: string) => {
      setState((prev) => ({ status: "checking", previous: lastReport(prev) }));
      try {
        const { data, error } = await client.POST("/api/v1/spec-checks", {
          params: { query: { ruleSets: sets } },
          body: document,
          bodySerializer: (body: string) => body,
          headers: { "Content-Type": "application/yaml" },
        });
        if (data) {
          setState({ status: "done", report: data });
          return;
        }
        setState((prev) => ({
          status: "failed",
          message: error?.detail ?? "The server couldn't check the document.",
          previous: lastReport(prev),
        }));
      } catch {
        setState((prev) => ({
          status: "failed",
          message: "Couldn't reach the Signalbox server. Start signalbox-server, then check again.",
          previous: lastReport(prev),
        }));
      }
    },
    [client],
  );

  // Open in a working state: check the sample document right away.
  useEffect(() => {
    void check(sampleSpec, ruleSetOptions[0].value);
  }, [check]);

  function revealLine(line: number) {
    const el = editor.current;
    if (!el || line < 1) return;
    const { start, end } = lineRange(el.value, line);
    el.focus();
    el.setSelectionRange(start, end);
    const lineHeight = Number.parseFloat(getComputedStyle(el).lineHeight) || 20;
    el.scrollTop = Math.max(0, (line - 4) * lineHeight);
  }

  const report = state.status === "done" ? state.report : lastReport(state);
  const checking = state.status === "checking";

  return (
    <div className="spec-check">
      <section className="panel editor-panel" aria-labelledby={`${ids.editor}-title`}>
        <div className="panel-head">
          <h2 id={`${ids.editor}-title`}>Check an OpenAPI document</h2>
          <p className="lede">
            Paste a Swagger 2.0 or OpenAPI 3.0, 3.1 or 3.2 document. Signalbox validates it against the official
            schema, resolves every <code>$ref</code> and applies the lint rules you choose.
          </p>
        </div>
        <label className="field-label" htmlFor={ids.editor}>
          Document (YAML or JSON)
        </label>
        <textarea
          id={ids.editor}
          ref={editor}
          className="editor"
          value={text}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
              e.preventDefault();
              void check(text, ruleSets);
            }
          }}
        />
        <div className="toolbar">
          <label className="field-label inline" htmlFor={ids.ruleSets}>
            Rules
          </label>
          <select id={ids.ruleSets} value={ruleSets} onChange={(e) => setRuleSets(e.target.value)}>
            {ruleSetOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <button type="button" className="primary" disabled={checking} onClick={() => void check(text, ruleSets)}>
            {checking ? "Checking…" : "Check document"}
          </button>
          <span className="shortcut">Ctrl+Enter</span>
        </div>
      </section>

      <section className="panel results-panel" aria-labelledby={ids.results} aria-busy={checking}>
        <div className="panel-head results-head">
          <h2 id={ids.results}>Problems</h2>
          <p className="summary" role="status" aria-live="polite">
            {report ? (
              <>
                {report.version ? <span className="format">OpenAPI {report.version}</span> : null}
                {summarize(report.counts)}
              </>
            ) : checking ? (
              "Checking…"
            ) : null}
          </p>
        </div>

        {state.status === "failed" ? (
          <p className="notice" role="alert">
            {state.message}
          </p>
        ) : null}

        {report && report.diagnostics.length > 0 ? (
          <ol className={`problems${checking ? " stale" : ""}`}>
            {report.diagnostics.map((d, i) => (
              <li key={`${d.code}-${d.range.start.line}-${d.range.start.column}-${i}`}>
                <button type="button" className="problem" onClick={() => revealLine(d.range.start.line)}>
                  <span className={`severity severity-${d.severity}`}>
                    <span className="severity-dot" aria-hidden="true" />
                    {severityLabels[d.severity]}
                  </span>
                  <span className="where">
                    {d.range.start.line > 0 ? `Line ${d.range.start.line}:${d.range.start.column}` : "Document"}
                  </span>
                  <span className="what">
                    <span className="message">{d.message}</span>
                    <span className="meta">
                      <code>{d.code}</code>
                      {d.howToFix ? <span className="fix">{d.howToFix}</span> : null}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ol>
        ) : report ? (
          <p className="empty">This document has no problems with the selected rules.</p>
        ) : null}
      </section>
    </div>
  );
}

function lastReport(state: CheckState): Report | undefined {
  switch (state.status) {
    case "done":
      return state.report;
    case "checking":
    case "failed":
      return state.previous;
    default:
      return undefined;
  }
}
