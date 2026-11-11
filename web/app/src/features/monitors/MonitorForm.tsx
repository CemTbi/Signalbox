// SPDX-License-Identifier: Apache-2.0

import type { Assertion, Environment, Item, Service } from "@signalbox/api-client";
import { useQuery } from "@tanstack/react-query";
import { type FormEvent, useId, useMemo } from "react";
import { api } from "../../api";
import { Field, FormAlert, fieldError } from "../../components/Form";
import { collectionsQuery, itemsQuery } from "../../queries";
import { AuthEditor, HeaderRows } from "../requests/RequestEditor";
import { methods } from "../requests/model";
import {
  type AssertionRow,
  assertionKinds,
  assertionRow,
  kindLabels,
  type MonitorDraft,
  opLabels,
  opsOf,
  sendsRequest,
  takesTarget,
  takesValue,
  withKind,
  withMonitorKind,
} from "./model";

const FIELDS = [
  "name",
  "kind",
  "environmentId",
  "serviceId",
  "request",
  "request/url",
  "assertions",
  "collectionId",
  "folderId",
  "intervalSeconds",
  "timeoutSeconds",
  "graceSeconds",
  "failuresToConfirm",
  "certificateWarningDays",
] as const;

const allKinds = ["http", "health", "collection", "heartbeat"] as const;

/**
 * A new monitor's settings, or an existing one's. A monitor's environment is
 * chosen once, and so is its kind, but for HTTP and health checks, which
 * change into each other.
 */
export function MonitorForm({
  draft,
  onChange,
  onSubmit,
  projectId,
  environments,
  services,
  pending,
  error,
  submitLabel,
  label,
  editing = false,
}: {
  draft: MonitorDraft;
  onChange: (d: MonitorDraft) => void;
  onSubmit: () => void;
  projectId: string;
  environments: Environment[];
  services: Service[];
  pending: boolean;
  error: unknown;
  submitLabel: string;
  label: string;
  /** An existing monitor's: its environment, and mostly its kind, stay. */
  editing?: boolean;
}) {
  const ids = { kind: useId(), env: useId(), service: useId(), method: useId(), url: useId(), body: useId() };
  const set = (change: Partial<MonitorDraft>) => onChange({ ...draft, ...change });
  const kinds = editing ? allKinds.filter((k) => (sendsRequest(draft.kind) ? sendsRequest(k) : k === draft.kind)) : allKinds;

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
          <select
            id={ids.kind}
            value={draft.kind}
            disabled={kinds.length === 1}
            onChange={(e) => onChange(withMonitorKind(draft, e.target.value as MonitorDraft["kind"]))}
          >
            {kinds.map((k) => (
              <option key={k} value={k}>
                {kindLabels[k]}
              </option>
            ))}
          </select>
        </div>
        {editing ? null : (
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
      {sendsRequest(draft.kind) ? <RequestFields draft={draft} set={set} error={error} ids={ids} /> : null}
      {draft.kind === "collection" ? <CollectionFields draft={draft} set={set} error={error} projectId={projectId} /> : null}
      {draft.kind === "heartbeat" ? (
        <p className="field-hint">
          A heartbeat sends nothing. The job it watches calls its URL when it finishes, and it goes down when no call comes within its interval and grace. The URL is shown once
          it&apos;s created.
        </p>
      ) : null}
      <NumberFields draft={draft} set={set} error={error} />
      <FormAlert error={error} fields={FIELDS} />
      <div>
        <button type="submit" className="button primary" disabled={pending}>
          {pending ? "Saving…" : submitLabel}
        </button>
      </div>
    </form>
  );
}

type Set = (change: Partial<MonitorDraft>) => void;

/** What an HTTP or health check sends, and what it asserts about the response. */
function RequestFields({ draft, set, error, ids }: { draft: MonitorDraft; set: Set; error: unknown; ids: { method: string; url: string; body: string } }) {
  const setRequest = (change: Partial<MonitorDraft["request"]>) => set({ request: { ...draft.request, ...change } });
  const setRow = (id: number, next: (r: AssertionRow) => AssertionRow) => set({ assertions: draft.assertions.map((a) => (a.id === id ? next(a) : a)) });
  const urlError = fieldError(error, "request/url");
  return (
    <>
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
    </>
  );
}

/** A folder's place in its collection, for choosing it: Orders / Refunds. */
function folderPaths(items: Item[]): { id: string; path: string }[] {
  const byId = new Map(items.map((i) => [i.id, i]));
  const pathOf = (i: Item): string => {
    const parent = i.parentId ? byId.get(i.parentId) : undefined;
    return parent ? `${pathOf(parent)} / ${i.name}` : i.name;
  };
  return items
    .filter((i) => i.kind === "folder")
    .map((i) => ({ id: i.id, path: pathOf(i) }))
    .sort((a, b) => a.path.localeCompare(b.path));
}

/** What a collection monitor runs: a collection, or one folder of it. */
function CollectionFields({ draft, set, error, projectId }: { draft: MonitorDraft; set: Set; error: unknown; projectId: string }) {
  const ids = { collection: useId(), folder: useId() };
  const collections = useQuery(collectionsQuery(api, projectId));
  const items = useQuery({ ...itemsQuery(api, draft.collectionId), enabled: draft.collectionId !== "" });
  const folders = useMemo(() => folderPaths(items.data?.data ?? []), [items.data]);
  const list = collections.data?.data ?? [];
  const collectionError = fieldError(error, "collectionId");
  const folderError = fieldError(error, "folderId");
  return (
    <>
      <div className="monitor-form-row">
        <div className="field">
          <label htmlFor={ids.collection}>Collection</label>
          <select
            id={ids.collection}
            value={draft.collectionId}
            required
            aria-invalid={collectionError ? true : undefined}
            onChange={(e) => set({ collectionId: e.target.value, folderId: "" })}
          >
            <option value="" disabled>
              {collections.isPending ? "Loading…" : list.length === 0 ? "No collections yet" : "Choose a collection"}
            </option>
            {list.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          {collectionError ? <p className="field-error">{collectionError}</p> : null}
        </div>
        <div className="field">
          <label htmlFor={ids.folder}>Run</label>
          <select id={ids.folder} value={draft.folderId} disabled={draft.collectionId === ""} onChange={(e) => set({ folderId: e.target.value })}>
            <option value="">The whole collection</option>
            {folders.map((f) => (
              <option key={f.id} value={f.id}>
                {f.path}
              </option>
            ))}
          </select>
          {folderError ? <p className="field-error">{folderError}</p> : null}
        </div>
      </div>
      <p className="field-hint">
        It passes when the run passes: every request gets a response, every test passes and no script fails. The environment&apos;s variables and secrets fill in{" "}
        {"{{name}}"}. Each run is kept, and the checks link to it.
      </p>
    </>
  );
}

/** How often, how long, and how sure: each kind has its own. */
function NumberFields({ draft, set, error }: { draft: MonitorDraft; set: Set; error: unknown }) {
  const failures = (
    <Field
      label="Failures in a row before it's down"
      type="number"
      min={1}
      max={10}
      value={draft.failuresToConfirm}
      onChange={(e) => set({ failuresToConfirm: Number(e.target.value) })}
      error={fieldError(error, "failuresToConfirm")}
    />
  );
  const interval = (
    <Field
      label="Check every (seconds)"
      type="number"
      min={30}
      max={3600}
      value={draft.intervalSeconds}
      onChange={(e) => set({ intervalSeconds: Number(e.target.value) })}
      error={fieldError(error, "intervalSeconds")}
    />
  );
  if (draft.kind === "heartbeat") {
    // Minutes, since jobs run every few minutes to every few weeks.
    return (
      <div className="monitor-form-row">
        <Field
          label="Expect a call every (minutes)"
          type="number"
          min={1}
          max={43200}
          value={draft.intervalSeconds / 60}
          onChange={(e) => set({ intervalSeconds: Math.round(Number(e.target.value) * 60) })}
          error={fieldError(error, "intervalSeconds")}
        />
        <Field
          label="Grace (minutes)"
          type="number"
          min={0}
          max={10080}
          value={draft.graceSeconds / 60}
          onChange={(e) => set({ graceSeconds: Math.round(Number(e.target.value) * 60) })}
          error={fieldError(error, "graceSeconds")}
        />
      </div>
    );
  }
  if (draft.kind === "collection") {
    return (
      <div className="monitor-form-row">
        {interval}
        <Field
          label="Run timeout (seconds)"
          type="number"
          min={1}
          max={120}
          value={draft.timeoutSeconds}
          onChange={(e) => set({ timeoutSeconds: Number(e.target.value) })}
          error={fieldError(error, "timeoutSeconds")}
        />
        {failures}
      </div>
    );
  }
  return (
    <div className="monitor-form-row">
      {interval}
      <Field
        label="Timeout (seconds)"
        type="number"
        min={1}
        max={30}
        value={draft.timeoutSeconds}
        onChange={(e) => set({ timeoutSeconds: Number(e.target.value) })}
        error={fieldError(error, "timeoutSeconds")}
      />
      {failures}
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
