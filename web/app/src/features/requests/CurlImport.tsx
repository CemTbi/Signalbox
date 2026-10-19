// SPDX-License-Identifier: Apache-2.0

import type { SavedRequest } from "@signalbox/api-client";
import { useMutation } from "@tanstack/react-query";
import { type FormEvent, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import type { Report } from "./ImportReport";

/** Reads a pasted curl command into the editor. */
export function CurlImport({ onLoad, onCancel }: { onLoad: (r: SavedRequest, report: Report) => void; onCancel: () => void }) {
  const field = useId();
  const [command, setCommand] = useState("");
  const read = useMutation({
    mutationFn: (c: string) => unwrap(api.POST("/api/v1/curl-imports", { body: { command: c } })),
    onSuccess: (out) => onLoad(out.request, out.report),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (command.trim()) read.mutate(command);
  }

  return (
    <form className="save-form form" onSubmit={submit} aria-label="Import a curl command">
      <div className="field">
        <label htmlFor={field}>curl command</label>
        <textarea
          id={field}
          className="mono"
          rows={6}
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="curl 'https://api.example.com/orders' -H 'accept: application/json'"
          spellCheck={false}
          required
        />
        <p className="field-hint">Paste a command, such as one from your browser's Copy as cURL, for bash or cmd. It replaces the request in the editor.</p>
      </div>
      {read.isError ? (
        <p className="form-alert" role="alert">
          {describeError(read.error)}
        </p>
      ) : null}
      <div className="form-actions">
        <button type="submit" className="button primary" disabled={read.isPending || !command.trim()}>
          {read.isPending ? "Reading…" : "Read command"}
        </button>
        <button type="button" className="button ghost" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
