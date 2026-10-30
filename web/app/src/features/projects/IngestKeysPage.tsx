// SPDX-License-Identifier: Apache-2.0

import type { IngestKey, NewIngestKey } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { Field, FormAlert, fieldError } from "../../components/Form";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, ingestKeysKey, ingestKeysQuery, projectQuery, workspaceQuery } from "../../queries";

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

const route = getRouteApi("/_app/projects/$projectId/ingest-keys");

/**
 * The keys a project's services send spans with, one environment at a
 * time. Owners and admins create and revoke them; everyone sees them.
 */
export function IngestKeysPage() {
  const { projectId } = route.useParams();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const environments = useQuery(environmentsQuery(api, projectId));
  const [chosen, setChosen] = useState("");
  const envId = chosen || environments.data?.data[0]?.id || "";
  const envSelect = useId();

  if (project.isPending || environments.isPending) return <Loading label="Loading ingest keys…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  if (environments.isError) return <LoadError message={describeError(environments.error)} onRetry={() => void environments.refetch()} />;

  const p = project.data;
  const role = workspace.data?.role;
  const canManage = role === "owner" || role === "admin";
  return (
    <div className="stack">
      <PageHeader
        title="Ingest keys"
        documentTitle={`Ingest keys · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId: p.id } },
          { label: "Ingest keys" },
        ]}
        meta={
          <p className="lede">
            Your services send their traces to Signalbox over OpenTelemetry or Zipkin, with a key that decides which environment they land in.
          </p>
        }
      />
      <div className="environment-bar">
        <label htmlFor={envSelect}>Environment</label>
        <select id={envSelect} value={envId} onChange={(e) => setChosen(e.target.value)}>
          {environments.data.data.map((e) => (
            <option key={e.id} value={e.id}>
              {e.name}
            </option>
          ))}
        </select>
      </div>
      {envId ? <EnvironmentKeys key={envId} environmentId={envId} canManage={canManage} /> : null}
    </div>
  );
}

function EnvironmentKeys({ environmentId, canManage }: { environmentId: string; canManage: boolean }) {
  const queryClient = useQueryClient();
  const keys = useQuery(ingestKeysQuery(api, environmentId));
  const [created, setCreated] = useState<NewIngestKey | null>(null);
  const create = useMutation({
    mutationFn: (name: string) =>
      unwrap(api.POST("/api/v1/environments/{environmentId}/ingest-keys", { params: { path: { environmentId } }, body: { name } })),
    onSuccess: async (k) => {
      setCreated(k);
      await queryClient.invalidateQueries({ queryKey: ingestKeysKey(environmentId) });
    },
  });
  const revoke = useMutation({
    mutationFn: (keyId: string) => unwrap(api.DELETE("/api/v1/ingest-keys/{keyId}", { params: { path: { keyId } } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ingestKeysKey(environmentId) }),
  });

  return (
    <div className="split">
      <section aria-labelledby="keys" className="split-main">
        <h2 id="keys" className="section-title">
          Keys
        </h2>
        {keys.isPending ? <Loading label="Loading keys…" /> : null}
        {keys.isError ? <LoadError message={describeError(keys.error)} onRetry={() => void keys.refetch()} /> : null}
        {revoke.isError ? (
          <p className="form-alert" role="alert">
            The key couldn't be revoked: {describeError(revoke.error)}
          </p>
        ) : null}
        {keys.data && keys.data.data.length === 0 ? (
          <div className="empty-state">
            <p className="empty-title">No keys yet</p>
            <p>Create one for each service or collector that sends spans, so you can revoke it without affecting the others.</p>
          </div>
        ) : null}
        {keys.data && keys.data.data.length > 0 ? (
          <ul className="rows">
            {keys.data.data.map((k) => (
              <KeyRow
                key={k.id}
                ingestKey={k}
                canManage={canManage}
                revoking={revoke.isPending && revoke.variables === k.id}
                onRevoke={() => revoke.mutate(k.id)}
              />
            ))}
          </ul>
        ) : null}
      </section>
      <section aria-labelledby="new-key" className="panel split-side">
        <h2 id="new-key" className="section-title">
          New key
        </h2>
        {!canManage ? (
          <p className="muted">Workspace owners and admins create ingest keys, since a key lets a service write to this project.</p>
        ) : created ? (
          <NewKey ingestKey={created} onDone={() => setCreated(null)} />
        ) : (
          <KeyForm pending={create.isPending} error={create.error} onSubmit={(name) => create.mutate(name)} />
        )}
      </section>
    </div>
  );
}

function KeyRow({ ingestKey: k, canManage, revoking, onRevoke }: { ingestKey: IngestKey; canManage: boolean; revoking: boolean; onRevoke: () => void }) {
  return (
    <li className="row">
      <span className="row-main">
        <span className="row-title">
          {k.name} <span className="mono muted">…{k.hint}</span>
        </span>
        <span className="row-sub">
          Created {dateFormat.format(new Date(k.createdAt))} · {k.lastUsedAt ? `last used ${dateFormat.format(new Date(k.lastUsedAt))}` : "never used"}
        </span>
      </span>
      {canManage ? (
        <span className="row-actions">
          <button type="button" className="button ghost" disabled={revoking} onClick={onRevoke} aria-label={`Revoke ${k.name}`}>
            {revoking ? "Revoking…" : "Revoke"}
          </button>
        </span>
      ) : null}
    </li>
  );
}

export function KeyForm({ pending, error, onSubmit }: { pending: boolean; error: unknown; onSubmit: (name: string) => void }) {
  const [name, setName] = useState("");
  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit(name);
  }
  return (
    <form className="form" onSubmit={submit} noValidate>
      <FormAlert error={error} fields={["name"]} />
      <Field
        label="Name"
        name="name"
        required
        maxLength={100}
        value={name}
        onChange={(e) => setName(e.target.value)}
        hint="What sends with it, such as checkout-service or the collector."
        error={fieldError(error, "name")}
      />
      <button type="submit" className="button primary" disabled={pending}>
        {pending ? "Creating…" : "Create key"}
      </button>
    </form>
  );
}

/** The OpenTelemetry settings that send to Signalbox with a key. */
export function exporterSettings(key: string, host: string): string {
  return [
    `OTEL_EXPORTER_OTLP_ENDPOINT=http://${host}:4318`,
    "OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
    // The variable's values are URL-encoded, so the space is %20.
    `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20${key}`,
  ].join("\n");
}

/**
 * Shows a new key once, with the settings that use it. Signalbox keeps
 * only its hash, so it can't be shown again.
 */
export function NewKey({ ingestKey, onDone }: { ingestKey: NewIngestKey; onDone: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState("");
  const keyId = useId();
  const settingsId = useId();
  const settings = exporterSettings(ingestKey.key, window.location.hostname);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);
  async function copy(what: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(what);
    } catch {
      input.current?.select();
    }
  }
  return (
    <div className="invitation-link" role="status">
      <p>
        Copy <strong>{ingestKey.name}</strong> now: you won't see it again. Services send it as <code>Authorization: Bearer …</code>.
      </p>
      <label htmlFor={keyId} className="field-label">
        Key
      </label>
      <div className="copy-row">
        <input id={keyId} ref={input} className="mono" readOnly value={ingestKey.key} onFocus={(e) => e.target.select()} />
        <button type="button" className="button secondary" onClick={() => void copy("key", ingestKey.key)}>
          {copied === "key" ? "Copied" : "Copy"}
        </button>
      </div>
      <label htmlFor={settingsId} className="field-label">
        OpenTelemetry settings
      </label>
      <textarea id={settingsId} className="mono" readOnly rows={3} value={settings} onFocus={(e) => e.target.select()} />
      <p className="field-hint">
        Port 4318 takes OTLP over HTTP, and 9411 takes Zipkin. If spans reach Signalbox at another address, use that instead.
      </p>
      <div className="form-actions">
        <button type="button" className="button secondary" onClick={() => void copy("settings", settings)}>
          {copied === "settings" ? "Copied" : "Copy settings"}
        </button>
        <button type="button" className="button ghost" onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  );
}
