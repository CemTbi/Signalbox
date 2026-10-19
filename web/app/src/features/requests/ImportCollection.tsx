// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type ChangeEvent, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { collectionsKey } from "../../queries";
import { ImportReport } from "./ImportReport";

/** Reads a chosen file as JSON, or says why it can't. */
export async function readJSONFile(file: File): Promise<Record<string, unknown>> {
  if (file.size > 25 * 1024 * 1024) throw new Error("The file is larger than 25 MiB, the most Signalbox imports.");
  try {
    return JSON.parse(await file.text()) as Record<string, unknown>;
  } catch {
    throw new Error("The file isn't JSON. Export it from Postman as a JSON file.");
  }
}

/**
 * Imports a Postman collection: choosing a file checks it and shows the
 * report, and Import stores it.
 */
export function ImportCollection({ projectId, onDone }: { projectId: string; onDone: () => void }) {
  const queryClient = useQueryClient();
  const fileField = useId();
  const [file, setFile] = useState<{ name: string; data: Record<string, unknown> } | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const call = (data: Record<string, unknown>, dryRun: boolean) =>
    unwrap(api.POST("/api/v1/projects/{projectId}/collection-imports", { params: { path: { projectId }, query: { dryRun } }, body: data }));
  const check = useMutation({ mutationFn: (data: Record<string, unknown>) => call(data, true) });
  const store = useMutation({
    mutationFn: (data: Record<string, unknown>) => call(data, false),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: collectionsKey(projectId) });
    },
  });

  async function choose(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    setFile(null);
    setReadError(null);
    check.reset();
    store.reset();
    if (!f) return;
    try {
      const data = await readJSONFile(f);
      setFile({ name: f.name, data });
      check.mutate(data);
    } catch (err) {
      setReadError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section className="import-panel" aria-label="Import a Postman collection">
      <div className="field">
        <label htmlFor={fileField}>Postman collection</label>
        <input id={fileField} type="file" accept=".json,application/json" onChange={(e) => void choose(e)} />
        <p className="field-hint">Export it from Postman as Collection v2.1. Scripts are kept, and never run while importing.</p>
      </div>
      {readError ? (
        <p className="form-alert" role="alert">
          {readError}
        </p>
      ) : null}
      {check.isPending ? <p className="field-hint">Checking {file?.name}…</p> : null}
      {check.isError ? (
        <p className="form-alert" role="alert">
          {describeError(check.error)}
        </p>
      ) : null}
      {store.isSuccess && store.data.report ? (
        <>
          <p className="field-hint" role="status">
            Imported {store.data.collection?.name}.
          </p>
          <ImportReport report={store.data.report} what="collection" />
        </>
      ) : check.data ? (
        <ImportReport report={check.data.report} what="collection" />
      ) : null}
      {store.isError ? (
        <p className="form-alert" role="alert">
          {describeError(store.error)}
        </p>
      ) : null}
      <div className="form-actions">
        {file && check.isSuccess && !store.isSuccess ? (
          <button type="button" className="button primary" disabled={store.isPending} onClick={() => store.mutate(file.data)}>
            {store.isPending ? "Importing…" : `Import ${file.name}`}
          </button>
        ) : null}
        <button type="button" className="button ghost" onClick={onDone}>
          {store.isSuccess ? "Done" : "Cancel"}
        </button>
      </div>
    </section>
  );
}
