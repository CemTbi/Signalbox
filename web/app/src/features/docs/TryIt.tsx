// SPDX-License-Identifier: Apache-2.0

import type { ReferenceOperation, ReferenceSecurityScheme } from "@signalbox/api-client";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { authorization, type ParameterValues, requestURL, sampleValue } from "./samples";

interface Result {
  status: number;
  statusText: string;
  ms: number;
  contentType: string;
  applied: string;
  body: string;
}

/** Sends a request to the mock, as the API's description says it would answer. */
export function TryIt({ op, mock, schemes }: { op: ReferenceOperation; mock: string; schemes: ReferenceSecurityScheme[] }) {
  const params = (op.parameters ?? []).filter((p) => p.in === "path" || p.in === "query" || p.in === "header");
  const auth = authorization(op, schemes);
  const media = op.requestBody?.content[0];
  const codes = op.responses.map((r) => r.code).filter((c) => /^\d{3}$/.test(c));
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(params.map((p) => [`${p.in} ${p.name}`, p.in === "path" || p.required ? sampleValue(p) : ""])),
  );
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [body, setBody] = useState(media?.examples?.[0]?.value ?? "");
  const [code, setCode] = useState("");
  const [result, setResult] = useState<Result | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const id = useId();

  useEffect(() => () => abort.current?.abort(), []);

  async function send(e: FormEvent) {
    e.preventDefault();
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    const v: ParameterValues = { path: {}, query: {}, header: {} };
    for (const p of params) {
      const value = values[`${p.in} ${p.name}`] ?? "";
      if (p.in === "path" || value !== "") v[p.in as keyof ParameterValues][p.name] = value;
    }
    const query = auth.query.map(([k], i): [string, string] => [k, credentials[`query ${i}`] ?? ""]).filter(([, value]) => value !== "");
    const headers = new Headers(v.header);
    auth.headers.forEach(([k], i) => {
      const value = credentials[`header ${i}`];
      if (value && k.toLowerCase() !== "cookie") headers.set(k, value);
    });
    if (code) headers.set("Prefer", `code=${code}`);
    const init: RequestInit = { method: op.method, headers, signal: controller.signal };
    if (media && op.method !== "GET" && op.method !== "HEAD") {
      headers.set("Content-Type", media.type);
      init.body = body;
    }
    setSending(true);
    setError(null);
    const started = performance.now();
    try {
      const response = await fetch(requestURL(mock, op.path, v, query), init);
      const text = await response.text();
      setResult({
        status: response.status,
        statusText: response.statusText,
        ms: Math.round(performance.now() - started),
        contentType: response.headers.get("Content-Type") ?? "",
        applied: response.headers.get("Preference-Applied") ?? "",
        body: pretty(text, response.headers.get("Content-Type") ?? ""),
      });
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? `The request didn't complete: ${err.message}` : "The request didn't complete.");
    } finally {
      if (abort.current === controller) setSending(false);
    }
  }

  return (
    <form className="try-it form" onSubmit={(e) => void send(e)} aria-label={`Try ${op.method} ${op.path}`}>
      {params.map((p) => {
        const key = `${p.in} ${p.name}`;
        return (
          <div key={key} className="field">
            <label htmlFor={`${id}-${key}`}>
              {p.name} <span className="muted">({p.in}{p.required || p.in === "path" ? ", required" : ""})</span>
            </label>
            <input
              id={`${id}-${key}`}
              className="mono"
              value={values[key] ?? ""}
              onChange={(e) => setValues({ ...values, [key]: e.target.value })}
            />
          </div>
        );
      })}
      {auth.headers.map(([name], i) =>
        name.toLowerCase() === "cookie" ? null : (
          <div key={`h${i}`} className="field">
            <label htmlFor={`${id}-auth-h${i}`}>
              {name} <span className="muted">(credentials)</span>
            </label>
            <input
              id={`${id}-auth-h${i}`}
              className="mono"
              value={credentials[`header ${i}`] ?? ""}
              placeholder={auth.headers[i]?.[1]}
              onChange={(e) => setCredentials({ ...credentials, [`header ${i}`]: e.target.value })}
            />
          </div>
        ),
      )}
      {auth.query.map(([name], i) => (
        <div key={`q${i}`} className="field">
          <label htmlFor={`${id}-auth-q${i}`}>
            {name} <span className="muted">(credentials)</span>
          </label>
          <input
            id={`${id}-auth-q${i}`}
            className="mono"
            value={credentials[`query ${i}`] ?? ""}
            onChange={(e) => setCredentials({ ...credentials, [`query ${i}`]: e.target.value })}
          />
        </div>
      ))}
      {media ? (
        <div className="field">
          <label htmlFor={`${id}-body`}>
            Body <span className="muted">({media.type})</span>
          </label>
          <textarea id={`${id}-body`} className="mono" rows={8} value={body} onChange={(e) => setBody(e.target.value)} spellCheck={false} />
        </div>
      ) : null}
      {codes.length > 1 ? (
        <div className="field">
          <label htmlFor={`${id}-code`}>Response</label>
          <select id={`${id}-code`} value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="">As the mock chooses</option>
            {codes.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
          <p className="field-hint">
            Sends <code>Prefer: code=…</code>, which skips checking the request.
          </p>
        </div>
      ) : null}
      <button type="submit" className="button primary" disabled={sending}>
        {sending ? "Sending…" : "Send"}
      </button>
      {error ? (
        <p className="form-alert" role="alert">
          {error}
        </p>
      ) : null}
      {result ? (
        <div className="try-result" aria-live="polite">
          <p className="schema-line">
            <span className={`status status-${String(result.status).charAt(0)}`}>{result.status}</span> {result.statusText} · {result.ms} ms
            {result.contentType ? ` · ${result.contentType}` : ""}
            {result.applied ? ` · applied ${result.applied}` : ""}
          </p>
          {result.body ? (
            <pre className="code">
              <code>{result.body}</code>
            </pre>
          ) : null}
        </div>
      ) : null}
    </form>
  );
}

function pretty(text: string, contentType: string) {
  if (!/json/i.test(contentType)) return text;
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
