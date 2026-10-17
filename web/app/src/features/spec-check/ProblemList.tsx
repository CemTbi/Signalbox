// SPDX-License-Identifier: Apache-2.0

import type { Diagnostic } from "@signalbox/api-client";
import { lineRange } from "./lines";

const severityLabels: Record<Diagnostic["severity"], string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
  hint: "Hint",
};

/**
 * A spec's problems, in document order. Each names its severity in words,
 * its line and column, its rule and how to fix it; choosing one shows the
 * line in the editor.
 */
export function ProblemList({
  diagnostics,
  onReveal,
  stale = false,
}: {
  diagnostics: Diagnostic[];
  onReveal: (line: number) => void;
  stale?: boolean;
}) {
  return (
    <ol className={`problems${stale ? " stale" : ""}`}>
      {diagnostics.map((d, i) => (
        <li key={`${d.code}-${d.range.start.line}-${d.range.start.column}-${i}`}>
          <button type="button" className="problem" onClick={() => onReveal(d.range.start.line)}>
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
  );
}

/** Selects a 1-based line in a textarea and scrolls it into view. */
export function revealLine(el: HTMLTextAreaElement | null, line: number) {
  if (!el || line < 1) return;
  const { start, end } = lineRange(el.value, line);
  el.focus();
  el.setSelectionRange(start, end);
  const lineHeight = Number.parseFloat(getComputedStyle(el).lineHeight) || 20;
  el.scrollTop = Math.max(0, (line - 4) * lineHeight);
}
