// SPDX-License-Identifier: Apache-2.0

import type { Assertion, Environment, Service } from "@signalbox/api-client";
import { type FormEvent, useId } from "react";
import { Field, FormAlert, fieldError } from "../../components/Form";
import { AuthEditor, HeaderRows } from "../requests/RequestEditor";
import { methods } from "../requests/model";
import { type AssertionRow, assertionKinds, assertionRow, type MonitorDraft, opLabels, opsOf, takesTarget, takesValue, withKind } from "./model";

const FIELDS = ["name", "kind", "environmentId", "serviceId", "request/url", "intervalSeconds", "timeoutSeconds", "failuresToConfirm", "certificateWarningDays"] as const;

/** A new monitor's settings, or an existing one's. A monitor's environment is chosen once. */
export function MonitorForm({
  draft,
  onChange,
  onSubmit,
  environments,
  services,
  pending,
  error,
  submitLabel,
  label,
  lockEnvironment = false,
}: {
  draft: MonitorDraft;
  onChange: (d: MonitorDraft) => void;
  onSubmit: () => void;
  environments: Environment[];
  services: Service[];
  pending: boolean;
  error: unknown;
  submitLabel: string;
  label: string;
  lockEnvironment?: boolean;
}) {
  const ids = { kind: useId(), env: useId(), service: useId(), method: useId(), url: useId(), body: useId() };
  const set = (change: Partial<MonitorDraft>) => onChange({ ...draft, ...change });
  const setRequest = (change: Partial<MonitorDraft["request"]>) => set({ request: { ...draft.request, ...change } });
  const setRow = (id: number, next: (r: AssertionRow) => AssertionRow) => set({ assertions: draft.assertions.map((a) => (a.id === id ? next(a) : a)) });
  const urlError = fieldError(error, "request/url");

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!pending) onSubmit();
  }
  return (
    <form className="stack-tight monitor-form" onSubmit={submit} aria-label={label}>
      <Field label="Name" value={draft.name} required onChange={(e) => set({ name: e.target.value })} error={fieldError(error, "name")} autoComplete="off" />
      <div className="monitor-form-row">
        <div className="field">
          <label htmlFor={ids.kind}>Kind</label>
          <select id={ids.kind} value={draft.kind} onChange={(e) => set({ kind: e.target.value as MonitorDraft["kind"] })}>
            <option value="http">HTTP check</option>
            <option value="health">Health check</option>
          </select>
        </div>
        {lockEnvironment ? null : (
          <div className="field">
            <label htmlFor={ids.env}>Environment</label>
            <select id={ids.env} value={draft.environmentId} onChange={(e) => set({ environmentId: e.target.value })}>
              {environments.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </select>
          </div>
        )}
        <div className="field">
          <label htmlFor={ids.service}>Service on the canvas</label>
          <select id={ids.service} value={draft.serviceId} onChange={(e) => set({ serviceId: e.target.value })}>
            <option value="">None</option>
            {services.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      {draft.kind === "health" ? (
        <p className="field-hint">
          Also reads application/health+json, Spring Boot Actuator&apos;s health or Kubernetes&apos; verbose /readyz. A warning, or a component that&apos;s down while the whole is up, makes it
          degraded.
        </p>
      ) : null}

      <div className="request-line">
        <label htmlFor={ids.method} className="visually-hidden">
          Method
        </label>
        <select id={ids.method} className="mono" value={draft.request.method} onChange={(e) => setRequest({ method: e.target.value })}>
          {methods.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <label htmlFor={ids.url} className="visually-hidden">
          URL
        </label>
        <input
          id={ids.url}
          className="mono"
          type="text"
          inputMode="url"
          required
          placeholder="https://api.example.com/health or {{baseUrl}}/health"
          value={draft.request.url}
          aria-invalid={urlError ? true : undefined}
          onChange={(e) => setRequest({ url: e.target.value })}
          spellCheck={false}
          autoComplete="off"
        />
      </div>
      {urlError ? <p className="field-error">{urlError}</p> : null}
      <p className="field-hint">The environment&apos;s variables and secrets fill in {"{{name}}"}. Checks are sent from this server, through its egress policy.</p>

      <details className="monitor-details">
        <summary>Headers, body and auth</summary>
        <div className="stack-tight">
          <HeaderRows draft={draft.request} onChange={(request) => set({ request })} />
          {draft.request.method !== "GET" && draft.request.method !== "HEAD" ? (
            <div className="field">
              <label htmlFor={ids.body}>Body</label>
              <textarea id={ids.body} className="mono" rows={4} value={draft.request.body} onChange={(e) => setRequest({ body: e.target.value })} spellCheck={false} />
            </div>
          ) : null}
          <AuthEditor auth={draft.request.auth} onChange={(auth) => setRequest({ auth })} />
        </div>
      </details>

      <fieldset className="monitor-assertions">
        <legend>Assertions</legend>
        <p className="field-hint">Unless a status is asserted, the status must be 2xx.</p>
        {draft.assertions.map((a, i) => (
          <AssertionEditor
            key={a.id}
            row={a}
            index={i + 1}
            error={fieldError(error, `assertions/${i}/target`) ?? fieldError(error, `assertions/${i}/value`) ?? fieldError(error, `assertions/${i}/op`)}
            onChange={(next) => setRow(a.id, next)}
            onRemove={() => set({ assertions: draft.assertions.filter((x) => x.id !== a.id) })}
          />
        ))}
        <button type="button" className="button secondary small" onClick={() => set({ assertions: [...draft.assertions, assertionRow()] })}>
          Add assertion
        </button>
      </fieldset>

      <div className="monitor-form-row">
        <Field
          label="Check every (seconds)"
          type="number"
          min={30}
          max={3600}
          value={draft.intervalSeconds}
          onChange={(e) => set({ intervalSeconds: Number(e.target.value) })}
          error={fieldError(error, "intervalSeconds")}
        />
        <Field
          label="Timeout (seconds)"
          type="number"
          min={1}
          max={30}
          value={draft.timeoutSeconds}
          onChange={(e) => set({ timeoutSeconds: Number(e.target.value) })}
          error={fieldError(error, "timeoutSeconds")}
        />
        <Field
          label="Failures in a row before it's down"
          type="number"
          min={1}
          max={10}
          value={draft.failuresToConfirm}
          onChange={(e) => set({ failuresToConfirm: Number(e.target.value) })}
          error={fieldError(error, "failuresToConfirm")}
        />
        <Field
          label="Warn days before a certificate expires"
          type="number"
          min={0}
          max={365}
          value={draft.certificateWarningDays}
          onChange={(e) => set({ certificateWarningDays: Number(e.target.value) })}
          error={fieldError(error, "certificateWarningDays")}
        />
      </div>
      <FormAlert error={error} fields={FIELDS} />
      <div>
        <button type="submit" className="button primary" disabled={pending}>
          {pending ? "Saving…" : submitLabel}
        </button>
      </div>
    </form>
  );
}

function AssertionEditor({
  row,
  index,
  error,
  onChange,
  onRemove,
}: {
  row: AssertionRow;
  index: number;
  error: string | undefined;
  onChange: (next: (r: AssertionRow) => AssertionRow) => void;
  onRemove: () => void;
}) {
  return (
    <div className="assertion-row">
      <select aria-label={`Assertion ${index} kind`} value={row.kind} onChange={(e) => onChange((r) => withKind(r, e.target.value as Assertion["kind"]))}>
        {assertionKinds.map((k) => (
          <option key={k.value} value={k.value}>
            {k.label}
          </option>
        ))}
      </select>
      {takesTarget(row.kind) ? (
        <input
          className="mono"
          aria-label={`Assertion ${index} ${row.kind === "header" ? "header name" : "JSONPath"}`}
          placeholder={row.kind === "header" ? "Content-Type" : "$.status"}
          value={row.target ?? ""}
          spellCheck={false}
          onChange={(e) => onChange((r) => ({ ...r, target: e.target.value }))}
        />
      ) : null}
      <select aria-label={`Assertion ${index} comparison`} value={row.op} onChange={(e) => onChange((r) => ({ ...r, op: e.target.value as Assertion["op"] }))}>
        {opsOf[row.kind].map((op) => (
          <option key={op} value={op}>
            {opLabels[op]}
          </option>
        ))}
      </select>
      {takesValue(row.op) ? (
        <input className="mono" aria-label={`Assertion ${index} value`} value={row.value ?? ""} spellCheck={false} onChange={(e) => onChange((r) => ({ ...r, value: e.target.value }))} />
      ) : null}
      <button type="button" className="button ghost small" aria-label={`Remove assertion ${index}`} onClick={onRemove}>
        Remove
      </button>
      {error ? <p className="field-error assertion-error">{error}</p> : null}
    </div>
  );
}
