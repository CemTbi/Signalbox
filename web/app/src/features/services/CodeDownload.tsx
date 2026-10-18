// SPDX-License-Identifier: Apache-2.0

import type { ApiClient, Version } from "@signalbox/api-client";
import { type FormEvent, useId, useState } from "react";
import { describeError, unwrap } from "../../api";
import { Field } from "../../components/Form";

export const codeTargets = [
  { id: "typescript-client", label: "TypeScript client", hint: "A module with a type per schema and a method per operation, on fetch, with no dependencies." },
  { id: "go-client", label: "Go client", hint: "A package with a type per schema and a method per operation, on net/http." },
  { id: "go-server", label: "Go server stub", hint: "A package with an interface to implement, typed requests and responses, and a net/http router." },
] as const;

type Target = (typeof codeTargets)[number]["id"];

/** Saves a downloaded file, as a link with a download attribute would. */
function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  // Let the download start before the URL goes.
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function fileName(disposition: string | null, fallback: string) {
  return /filename="([^"]+)"/.exec(disposition ?? "")?.[1] ?? fallback;
}

/** Downloads code generated from a published version. */
export function CodeDownload({ client, versions }: { client: ApiClient; versions: Version[] }) {
  const [versionId, setVersionId] = useState(versions[0]?.id ?? "");
  const [target, setTarget] = useState<Target>("typescript-client");
  const [pkg, setPkg] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ name: string; warnings: number } | null>(null);
  const ids = { heading: useId(), version: useId(), target: useId() };
  const chosen = versions.find((v) => v.id === versionId) ?? versions[0];
  const hint = codeTargets.find((t) => t.id === target)?.hint;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!chosen) return;
    setPending(true);
    setError(null);
    setDone(null);
    try {
      const call = client.GET("/api/v1/versions/{versionId}/code", {
        params: { path: { versionId: chosen.id }, query: { target, ...(pkg ? { package: pkg } : {}) } },
        parseAs: "blob",
      });
      const { response } = await call;
      const blob = (await unwrap(call)) as Blob;
      const name = fileName(response.headers.get("Content-Disposition"), `${target}-${chosen.name}.zip`);
      save(blob, name);
      setDone({ name, warnings: Number(response.headers.get("X-Codegen-Warnings") ?? "0") });
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  if (versions.length === 0) return null;
  return (
    <section aria-labelledby={ids.heading} className="panel">
      <h2 id={ids.heading} className="section-title">
        Generate code
      </h2>
      <form className="form code-form" onSubmit={(e) => void submit(e)} noValidate>
        <div className="field">
          <label htmlFor={ids.version}>Version</label>
          <select id={ids.version} value={chosen?.id ?? ""} onChange={(e) => setVersionId(e.target.value)}>
            {versions.map((v, i) => (
              <option key={v.id} value={v.id}>
                {v.name}
                {i === 0 ? " (latest)" : ""}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor={ids.target}>Generate</label>
          <select id={ids.target} value={target} onChange={(e) => setTarget(e.target.value as Target)} aria-describedby={`${ids.target}-hint`}>
            {codeTargets.map((t) => (
              <option key={t.id} value={t.id}>
                {t.label}
              </option>
            ))}
          </select>
          <p id={`${ids.target}-hint`} className="field-hint">
            {hint}
          </p>
        </div>
        <Field
          label="Package name (optional)"
          name="package"
          className="mono"
          maxLength={64}
          value={pkg}
          onChange={(e) => setPkg(e.target.value.trim())}
          hint="Lowercase letters and digits. Without one, the package is named after the API."
        />
        <button type="submit" className="button primary" disabled={pending}>
          {pending ? "Generating…" : "Download zip"}
        </button>
        {error ? (
          <p className="form-alert" role="alert">
            {error}
          </p>
        ) : null}
        {done ? (
          <p className="field-hint" role="status">
            Downloaded {done.name}.{" "}
            {done.warnings > 0
              ? `${done.warnings} ${done.warnings === 1 ? "part" : "parts"} of the spec can't be expressed in this code; WARNINGS.md in the archive lists them.`
              : "Generate it again whenever you publish a new version."}
          </p>
        ) : null}
      </form>
    </section>
  );
}
