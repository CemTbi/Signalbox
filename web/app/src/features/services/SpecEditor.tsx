// SPDX-License-Identifier: Apache-2.0

import type { Report } from "@signalbox/api-client";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { ApiError, describeError } from "../../api";
import { summarize } from "../spec-check/lines";
import { ProblemList, revealLine } from "../spec-check/ProblemList";

/**
 * A spec's current document and its problems. People who may edit change
 * the text and save it as a new revision; the problems are those of the
 * saved revision.
 */
export function SpecEditor({
  content,
  report,
  canEdit,
  saving,
  error,
  onSave,
  onReload,
}: {
  /** The current revision's document. */
  content: string;
  /** The current revision's check report. */
  report: Report;
  canEdit: boolean;
  saving: boolean;
  error: unknown;
  onSave: (content: string, message: string) => void;
  /** Loads the newest revision, dropping unsaved changes. */
  onReload: () => void;
}) {
  const [text, setText] = useState(content);
  const [message, setMessage] = useState("");
  const editor = useRef<HTMLTextAreaElement>(null);
  const ids = { editor: useId(), message: useId(), problems: useId() };

  // A newer revision replaces the text, but only once it is saved or
  // reloaded: content changes then, and unsaved typing is never lost.
  useEffect(() => {
    setText(content);
    setMessage("");
  }, [content]);

  const dirty = text !== content;
  const conflict = error instanceof ApiError && error.code === "edit-conflict";

  function submit(e: FormEvent) {
    e.preventDefault();
    onSave(text, message);
  }

  return (
    <div className="spec-check">
      <section className="panel editor-panel" aria-labelledby={`${ids.editor}-title`}>
        <div className="panel-head">
          <h2 id={`${ids.editor}-title`}>Document</h2>
          {dirty ? <p className="lede">Unsaved changes</p> : null}
        </div>
        <form onSubmit={submit} className="form">
          {conflict ? (
            <div className="form-alert" role="alert">
              <p>Someone saved a newer version while you were editing. Copy your changes, then load the newest version.</p>
              <button type="button" className="button secondary" onClick={onReload}>
                Load the newest version
              </button>
            </div>
          ) : error ? (
            <p className="form-alert" role="alert">
              {describeError(error)}
            </p>
          ) : null}
          <label className="field-label" htmlFor={ids.editor}>
            Document (YAML or JSON)
          </label>
          <textarea
            id={ids.editor}
            ref={editor}
            className="editor"
            value={text}
            readOnly={!canEdit}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            onChange={(e) => setText(e.target.value)}
          />
          {canEdit ? (
            <div className="toolbar">
              <label className="field-label inline" htmlFor={ids.message}>
                What changed
              </label>
              <input id={ids.message} value={message} maxLength={500} onChange={(e) => setMessage(e.target.value)} />
              <button type="submit" className="button primary" disabled={!dirty || saving}>
                {saving ? "Checking and saving…" : "Save revision"}
              </button>
            </div>
          ) : null}
        </form>
      </section>

      <section className="panel results-panel" aria-labelledby={ids.problems}>
        <div className="panel-head results-head">
          <h2 id={ids.problems}>Problems in the saved version</h2>
          <p className="summary">
            {report.version ? <span className="format">OpenAPI {report.version}</span> : null}
            {summarize(report.counts)}
          </p>
        </div>
        {report.diagnostics.length > 0 ? (
          <ProblemList diagnostics={report.diagnostics} onReveal={(line) => revealLine(editor.current, line)} stale={dirty} />
        ) : (
          <p className="empty">The saved version has no problems with the recommended rules.</p>
        )}
      </section>
    </div>
  );
}
