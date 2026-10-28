// SPDX-License-Identifier: Apache-2.0

import type { Environment, EnvironmentVariables } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ChangeEvent, type FormEvent, useId, useState } from "react";
import { api, ApiError, describeError, unwrapTagged } from "../../api";
import { LoadError, Loading } from "../../components/Page";
import { variablesKey, variablesQuery } from "../../queries";
import { readJSONFile } from "./ImportCollection";
import { ImportReport } from "./ImportReport";

/** A variable row in the editor. */
interface VariableRow {
  id: number;
  key: string;
  value: string;
  secret: boolean;
  /** A stored secret: an empty value keeps it. */
  stored: boolean;
}

let nextRow = 1;

function variableRow(key = "", value = "", secret = false, stored = false): VariableRow {
  return { id: nextRow++, key, value, secret, stored };
}

function rowsFrom(vs: EnvironmentVariables): VariableRow[] {
  return [...vs.variables.map((v) => variableRow(v.key, v.value ?? "", v.secret, v.secret)), variableRow()];
}

/** The variables to store: named rows, with a stored secret's value left out to keep it. */
export function toPut(rows: VariableRow[]) {
  return rows
    .filter((r) => r.key.trim() !== "")
    .map((r) => (r.secret && r.stored && r.value === "" ? { key: r.key.trim(), secret: true } : { key: r.key.trim(), value: r.value, secret: r.secret }));
}

/** An environment's variables, which fill in {{name}} when a request is sent. */
export function VariablesEditor({ environment, canEdit, onClose }: { environment: Environment; canEdit: boolean; onClose: () => void }) {
  const variables = useQuery(variablesQuery(api, environment.id));
  // Kept here, since saving starts the form over.
  const [saved, setSaved] = useState(false);
  return (
    <section className="variables" aria-label={`${environment.name} variables`}>
      <div className="variables-head">
        <h2>{environment.name} variables</h2>
        <button type="button" className="button ghost small" onClick={onClose}>
          Close
        </button>
      </div>
      {canEdit ? <EnvironmentImport environment={environment} /> : null}
      {variables.isPending ? <Loading label="Loading variables…" /> : null}
      {variables.isError ? <LoadError message={describeError(variables.error)} onRetry={() => void variables.refetch()} /> : null}
      {variables.isSuccess ? (
        // A new version starts the form over from what's stored.
        <VariablesForm key={variables.data.etag} environment={environment} stored={variables.data} canEdit={canEdit} saved={saved} onSaved={setSaved} />
      ) : null}
    </section>
  );
}

function VariablesForm({
  environment,
  stored,
  canEdit,
  saved,
  onSaved,
}: {
  environment: Environment;
  stored: { data: EnvironmentVariables; etag: string };
  canEdit: boolean;
  saved: boolean;
  onSaved: (saved: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [rows, setRows] = useState(() => rowsFrom(stored.data));
  const save = useMutation({
    mutationFn: () =>
      unwrapTagged(
        api.PUT("/api/v1/environments/{environmentId}/variables", {
          params: { path: { environmentId: environment.id }, header: { "If-Match": stored.etag } },
          body: { variables: toPut(rows) },
        }),
      ),
    onSuccess: (next) => {
      onSaved(true);
      queryClient.setQueryData(variablesKey(environment.id), next);
    },
  });
  const conflict = save.error instanceof ApiError && save.error.status === 412;

  function set(id: number, change: Partial<VariableRow>) {
    const next = rows.map((r) => (r.id === id ? { ...r, ...change } : r));
    // A row is always free for the next variable.
    if (next.at(-1)?.key || next.at(-1)?.value) next.push(variableRow());
    setRows(next);
    onSaved(false);
  }
  function submit(e: FormEvent) {
    e.preventDefault();
    if (canEdit) save.mutate();
  }

  return (
    <form className="form" onSubmit={submit} aria-label={`${environment.name} variables`}>
      <p className="field-hint">
        Write <code>{"{{name}}"}</code> in a URL, header or body, and sending with {environment.name} fills it in. Secrets are encrypted, never shown again,
        and hidden in history.
      </p>
      <table className="header-rows">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Value</th>
            <th scope="col">Secret</th>
            <th scope="col">
              <span className="visually-hidden">Remove</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.id}>
              <td>
                <input
                  className="mono"
                  value={r.key}
                  placeholder="baseUrl"
                  aria-label={`Variable ${i + 1} name`}
                  readOnly={!canEdit}
                  spellCheck={false}
                  onChange={(e) => set(r.id, { key: e.target.value })}
                />
              </td>
              <td>
                <input
                  className="mono"
                  type={r.secret ? "password" : "text"}
                  value={r.value}
                  placeholder={r.stored ? "Stored. Type to replace it." : "Value"}
                  aria-label={`Variable ${i + 1} value`}
                  readOnly={!canEdit}
                  spellCheck={false}
                  autoComplete="off"
                  onChange={(e) => set(r.id, { value: e.target.value })}
                />
              </td>
              <td>
                <input
                  type="checkbox"
                  checked={r.secret}
                  disabled={!canEdit}
                  aria-label={`Variable ${i + 1} is a secret`}
                  onChange={(e) => set(r.id, { secret: e.target.checked, stored: e.target.checked && r.stored })}
                />
              </td>
              <td>
                {canEdit && i < rows.length - 1 ? (
                  <button
                    type="button"
                    className="button ghost small"
                    aria-label={`Remove variable ${r.key || i + 1}`}
                    onClick={() => {
                      setRows(rows.filter((x) => x.id !== r.id));
                      onSaved(false);
                    }}
                  >
                    Remove
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {save.isError ? (
        <p className="form-alert" role="alert">
          {conflict ? "Someone changed these variables since you opened them. Close and open them again, then make your change." : describeError(save.error)}
        </p>
      ) : null}
      {canEdit ? (
        <div className="form-actions">
          <button type="submit" className="button primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save variables"}
          </button>
          {saved ? (
            <span className="field-hint" role="status">
              Saved.
            </span>
          ) : null}
        </div>
      ) : (
        <p className="field-hint">Viewers can see variables but not change them.</p>
      )}
    </form>
  );
}

/** Adds a Postman environment file's variables to the environment. */
function EnvironmentImport({ environment }: { environment: Environment }) {
  const queryClient = useQueryClient();
  const field = useId();
  const [readError, setReadError] = useState<string | null>(null);
  const run = useMutation({
    mutationFn: async (data: Record<string, unknown>) => {
      const call = api.POST("/api/v1/environments/{environmentId}/variable-imports", { params: { path: { environmentId: environment.id } }, body: data });
      return unwrapTagged(call);
    },
    onSuccess: ({ data, etag }) => {
      if (data.variables) queryClient.setQueryData(variablesKey(environment.id), { data: data.variables, etag });
    },
  });

  async function choose(e: ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    e.target.value = "";
    setReadError(null);
    run.reset();
    if (!f) return;
    try {
      run.mutate(await readJSONFile(f));
    } catch (err) {
      setReadError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="environment-import">
      <div className="field">
        <label htmlFor={field}>Import a Postman environment</label>
        <input id={field} type="file" accept=".json,application/json" disabled={run.isPending} onChange={(e) => void choose(e)} />
        <p className="field-hint">Its variables join these; one with the same name replaces it. Postman's secrets stay secret.</p>
      </div>
      {readError || run.isError ? (
        <p className="form-alert" role="alert">
          {readError ?? describeError(run.error)}
        </p>
      ) : null}
      {run.data ? <ImportReport report={run.data.data.report} what="environment" /> : null}
    </div>
  );
}
