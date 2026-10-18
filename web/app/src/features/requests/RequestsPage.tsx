// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry, Item } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { api, ApiError, describeError, unwrap, unwrapTagged } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { environmentsQuery, historyKey, historyQuery, itemsKey, projectQuery, workspaceQuery } from "../../queries";
import { CollectionsPanel } from "./CollectionsPanel";
import { type Draft, emptyDraft, formatMs, fromEntry, fromSaved, hadCredentials, toSaved, toSend } from "./model";
import { RequestEditor } from "./RequestEditor";
import { ResponseView } from "./ResponseView";
import { type SaveTarget, SaveForm } from "./SaveForm";
import { VariablesEditor } from "./VariablesEditor";

const requestsRoute = getRouteApi("/_app/projects/$projectId/requests");

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" });

/** The saved request open in the editor. */
interface OpenItem {
  id: string;
  collectionId: string;
  name: string;
  etag: string;
}

/** The environment someone last sent with in a project, kept in their browser. */
function rememberedEnvironment(projectId: string): string {
  try {
    return localStorage.getItem(`signalbox.environment.${projectId}`) ?? "";
  } catch {
    return "";
  }
}

function rememberEnvironment(projectId: string, environmentId: string) {
  try {
    localStorage.setItem(`signalbox.environment.${projectId}`, environmentId);
  } catch {
    // Remembering is a convenience; without storage the choice lasts the visit.
  }
}

/** A project's API client: saved requests, sending, and history. */
export function RequestsPage() {
  const { projectId } = requestsRoute.useParams();
  const queryClient = useQueryClient();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const history = useInfiniteQuery(historyQuery(api, projectId));
  const environments = useQuery(environmentsQuery(api, projectId));
  const [environmentId, setEnvironmentId] = useState(() => rememberedEnvironment(projectId));
  const [editingVariables, setEditingVariables] = useState(false);
  const environmentField = useId();
  const environment = environments.data?.data.find((e) => e.id === environmentId);
  useEffect(() => {
    // An environment that's gone, or another project's, isn't chosen.
    if (environments.isSuccess && environmentId !== "" && !environment) setEnvironmentId("");
  }, [environments.isSuccess, environmentId, environment]);
  const [panel, setPanel] = useState<"collections" | "history">("collections");
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [shown, setShown] = useState<HistoryEntry | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [openItem, setOpenItem] = useState<OpenItem | null>(null);
  const [saving, setSaving] = useState(false);

  const send = useMutation({
    mutationFn: (d: Draft) =>
      unwrap(
        api.POST("/api/v1/projects/{projectId}/send", {
          params: { path: { projectId } },
          body: { ...toSend(d), ...(environment ? { environmentId: environment.id } : {}) },
        }),
      ),
    onSuccess: async (entry) => {
      setShown(entry);
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: historyKey(projectId) });
    },
  });
  const openEntry = useMutation({
    mutationFn: (entryId: string) => unwrap(api.GET("/api/v1/history/{entryId}", { params: { path: { entryId } } })),
    onSuccess: (entry) => {
      setDraft(fromEntry(entry));
      setShown(entry);
      setOpenItem(null);
      setNotice(hadCredentials(entry) ? "Credentials aren't kept in history. Type them again before sending." : null);
    },
  });
  const openSaved = useMutation({
    mutationFn: (item: Item) => unwrapTagged(api.GET("/api/v1/items/{itemId}", { params: { path: { itemId: item.id } } })),
    onSuccess: ({ data, etag }) => {
      if (data.request) setDraft(fromSaved(data.request));
      setOpenItem({ id: data.id, collectionId: data.collectionId, name: data.name, etag });
      setShown(null);
      setNotice(null);
      setSaving(false);
    },
  });
  const save = useMutation({
    mutationFn: (item: OpenItem) =>
      unwrapTagged(
        api.PATCH("/api/v1/items/{itemId}", {
          params: { path: { itemId: item.id }, header: { "If-Match": item.etag } },
          body: { request: toSaved(draft) },
        }),
      ),
    onSuccess: async ({ data, etag }) => {
      setOpenItem({ id: data.id, collectionId: data.collectionId, name: data.name, etag });
      await queryClient.invalidateQueries({ queryKey: itemsKey(data.collectionId) });
    },
  });
  const saveAs = useMutation({
    mutationFn: (t: SaveTarget) =>
      unwrapTagged(
        api.POST("/api/v1/collections/{collectionId}/items", {
          params: { path: { collectionId: t.collectionId } },
          body: { kind: "request", name: t.name, request: toSaved(draft), ...(t.parentId ? { parentId: t.parentId } : {}) },
        }),
      ),
    onSuccess: async ({ data, etag }) => {
      setOpenItem({ id: data.id, collectionId: data.collectionId, name: data.name, etag });
      setSaving(false);
      setPanel("collections");
      await queryClient.invalidateQueries({ queryKey: itemsKey(data.collectionId) });
    },
  });

  if (project.isPending) return <Loading label="Loading project…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  const p = project.data;
  const canSend = workspace.data !== undefined && workspace.data.role !== "viewer";
  const entries = history.data?.pages.flatMap((page) => page.data) ?? [];
  const conflict = save.error instanceof ApiError && save.error.status === 412;

  return (
    <div className="stack">
      <PageHeader
        title={openItem ? openItem.name : "Requests"}
        documentTitle={`Requests · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId } },
          { label: "Requests" },
        ]}
        actions={
          canSend ? (
            <div className="row-actions">
              {openItem ? (
                <button type="button" className="button secondary" disabled={save.isPending} onClick={() => save.mutate(openItem)}>
                  {save.isPending ? "Saving…" : "Save"}
                </button>
              ) : null}
              <button type="button" className="button ghost" onClick={() => setSaving(true)}>
                Save as…
              </button>
              {openItem ? (
                <button
                  type="button"
                  className="button ghost"
                  onClick={() => {
                    setOpenItem(null);
                    setDraft(emptyDraft());
                    setShown(null);
                  }}
                >
                  New request
                </button>
              ) : null}
            </div>
          ) : null
        }
      />
      <div className="environment-bar">
        <label htmlFor={environmentField}>Environment</label>
        <select
          id={environmentField}
          value={environment?.id ?? ""}
          onChange={(e) => {
            setEnvironmentId(e.target.value);
            rememberEnvironment(projectId, e.target.value);
            if (e.target.value === "") setEditingVariables(false);
          }}
        >
          <option value="">No environment</option>
          {environments.data?.data.map((env) => (
            <option key={env.id} value={env.id}>
              {env.name}
            </option>
          ))}
        </select>
        {environment ? (
          <button type="button" className="button ghost small" aria-expanded={editingVariables} onClick={() => setEditingVariables(!editingVariables)}>
            {editingVariables ? "Hide variables" : "Variables"}
          </button>
        ) : (
          <span className="field-hint">Choose an environment to fill in {"{{variables}}"}.</span>
        )}
        {environment?.kind === "production" ? <span className="environment-kind">Production</span> : null}
      </div>
      {environments.isError ? <LoadError message={describeError(environments.error)} onRetry={() => void environments.refetch()} /> : null}
      {environment && editingVariables ? (
        <VariablesEditor key={environment.id} environment={environment} canEdit={canSend} onClose={() => setEditingVariables(false)} />
      ) : null}
      <div className="requests">
        <aside className="requests-side">
          <div className="tabs" role="tablist" aria-label="Saved and sent requests">
            <button type="button" role="tab" aria-selected={panel === "collections"} onClick={() => setPanel("collections")}>
              Collections
            </button>
            <button type="button" role="tab" aria-selected={panel === "history"} onClick={() => setPanel("history")}>
              History
            </button>
          </div>
          {panel === "collections" ? (
            <section aria-label="Collections" role="tabpanel">
              <CollectionsPanel projectId={projectId} canEdit={canSend} openItemId={openItem?.id} onOpen={(item) => openSaved.mutate(item)} />
            </section>
          ) : (
            <section aria-label="Your history" role="tabpanel" className="requests-history">
              {history.isError ? <LoadError message={describeError(history.error)} onRetry={() => void history.refetch()} /> : null}
              {history.isSuccess && entries.length === 0 ? <p className="field-hint">Requests you send appear here for 30 days.</p> : null}
              <ul className="history-list">
                {entries.map((e) => (
                  <li key={e.id}>
                    <button type="button" className="history-item" onClick={() => openEntry.mutate(e.id)} aria-current={shown?.id === e.id ? "true" : undefined}>
                      <span className={`method method-${e.method.toLowerCase()}`}>{e.method}</span>
                      <span className="history-url mono">{e.url}</span>
                      <span className="history-meta">
                        {e.status ? <span className={`status status-${String(e.status).charAt(0)}`}>{e.status}</span> : <span className="status status-5">Failed</span>}{" "}
                        {formatMs(e.durationMs)} · {timeFormat.format(new Date(e.sentAt))}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
              {history.hasNextPage ? (
                <button type="button" className="button secondary small" disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
                  {history.isFetchingNextPage ? "Loading…" : "Older requests"}
                </button>
              ) : null}
            </section>
          )}
        </aside>
        <div className="requests-main">
          {!canSend && workspace.data ? (
            <p className="notice">Viewers can open saved requests and their history, but can't send or save. Ask a workspace owner or admin to make you an editor.</p>
          ) : null}
          {notice ? <p className="notice">{notice}</p> : null}
          {shown?.unresolved?.length ? (
            <p className="notice">
              {shown.unresolved.length === 1 ? "This name had" : "These names had"} no value, so {shown.unresolved.length === 1 ? "it was" : "they were"} sent as
              typed: {shown.unresolved.map((n) => `{{${n}}}`).join(", ")}.
              {environment ? ` Add ${shown.unresolved.length === 1 ? "it" : "them"} under ${environment.name} variables.` : " Choose an environment that has them."}
            </p>
          ) : null}
          {saving ? (
            <SaveForm
              projectId={projectId}
              suggestedName={openItem ? `${openItem.name} (copy)` : draft.url.replace(/^https?:\/\//, "").slice(0, 80) || "New request"}
              pending={saveAs.isPending}
              error={saveAs.error}
              onSave={(t) => saveAs.mutate(t)}
              onCancel={() => setSaving(false)}
            />
          ) : null}
          {save.isError ? (
            <p className="form-alert" role="alert">
              {conflict ? "Someone changed this request since you opened it. Open it again from Collections, then make your change." : describeError(save.error)}
            </p>
          ) : null}
          <RequestEditor draft={draft} onChange={setDraft} onSend={() => send.mutate(draft)} sending={send.isPending} canSend={canSend} />
          {[send, openEntry, openSaved].map((m, i) =>
            m.isError ? (
              <p key={i} className="form-alert" role="alert">
                {describeError(m.error)}
              </p>
            ) : null,
          )}
          {shown ? <ResponseView key={shown.id} entry={shown} /> : <p className="field-hint">Send a request to see its response here.</p>}
        </div>
      </div>
    </div>
  );
}
