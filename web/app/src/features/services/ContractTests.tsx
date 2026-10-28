// SPDX-License-Identifier: Apache-2.0

import type { Version } from "@signalbox/api-client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type FormEvent, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { collectionsKey } from "../../queries";

/** Generates contract tests from a published version, to run as a suite. */
export function ContractTests({ versions, projectId, canEdit }: { versions: Version[]; projectId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const [versionId, setVersionId] = useState(versions[0]?.id ?? "");
  const ids = { heading: useId(), version: useId() };
  const chosen = versions.find((v) => v.id === versionId) ?? versions[0];
  const generate = useMutation({
    mutationFn: (id: string) => unwrap(api.POST("/api/v1/versions/{versionId}/contract-tests", { params: { path: { versionId: id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: collectionsKey(projectId) }),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    if (chosen) generate.mutate(chosen.id);
  }

  return (
    <section aria-labelledby={ids.heading} className="panel">
      <h2 id={ids.heading} className="section-title">
        Contract tests
      </h2>
      <p className="field-hint">
        A collection with a request per operation, filled from the spec's examples. Each checks that the response has a documented status and matches its
        schema. Generating again for a newer version replaces the generated requests, and keeps what you add and the variables' values.
      </p>
      {versions.length === 0 ? (
        <p className="field-hint">Publish a version to generate contract tests from it.</p>
      ) : !canEdit ? (
        <p className="field-hint">Editors can generate contract tests.</p>
      ) : (
        <form className="form code-form" onSubmit={submit}>
          <div className="field">
            <label htmlFor={ids.version}>Version</label>
            <select id={ids.version} value={chosen?.id ?? ""} onChange={(e) => setVersionId(e.target.value)}>
              {versions.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.name}
                </option>
              ))}
            </select>
          </div>
          <button type="submit" className="button primary" disabled={generate.isPending}>
            {generate.isPending ? "Generating…" : "Generate contract tests"}
          </button>
        </form>
      )}
      {generate.isError ? (
        <p className="form-alert" role="alert">
          {describeError(generate.error)}
        </p>
      ) : null}
      {generate.data ? (
        <p className="notice" role="status">
          {generate.data.report.created ? "Made" : "Updated"} {generate.data.collection.name} with {generate.data.report.requests} request
          {generate.data.report.requests === 1 ? "" : "s"}.{" "}
          <Link to="/collections/$collectionId/runs" params={{ collectionId: generate.data.collection.id }}>
            Run them
          </Link>
        </p>
      ) : null}
    </section>
  );
}
