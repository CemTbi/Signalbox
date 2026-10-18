// SPDX-License-Identifier: Apache-2.0

import type { HistoryEntry } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { historyKey, historyQuery, projectQuery, workspaceQuery } from "../../queries";
import { type Draft, emptyDraft, formatMs, fromEntry, hadCredentials, toSend } from "./model";
import { RequestEditor } from "./RequestEditor";
import { ResponseView } from "./ResponseView";

const requestsRoute = getRouteApi("/_app/projects/$projectId/requests");

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" });

/** A project's API client: send a request and read what comes back. */
export function RequestsPage() {
  const { projectId } = requestsRoute.useParams();
  const queryClient = useQueryClient();
  const project = useQuery(projectQuery(api, projectId));
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const history = useInfiniteQuery(historyQuery(api, projectId));
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [shown, setShown] = useState<HistoryEntry | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const send = useMutation({
    mutationFn: (d: Draft) =>
      unwrap(api.POST("/api/v1/projects/{projectId}/send", { params: { path: { projectId } }, body: toSend(d) })),
    onSuccess: async (entry) => {
      setShown(entry);
      setNotice(null);
      await queryClient.invalidateQueries({ queryKey: historyKey(projectId) });
    },
  });
  const open = useMutation({
    mutationFn: (entryId: string) => unwrap(api.GET("/api/v1/history/{entryId}", { params: { path: { entryId } } })),
    onSuccess: (entry) => {
      setDraft(fromEntry(entry));
      setShown(entry);
      setNotice(hadCredentials(entry) ? "Credentials aren't kept in history. Type them again before sending." : null);
    },
  });

  if (project.isPending) return <Loading label="Loading project…" />;
  if (project.isError) return <LoadError message={describeError(project.error)} onRetry={() => void project.refetch()} />;
  const p = project.data;
  const canSend = workspace.data !== undefined && workspace.data.role !== "viewer";
  const entries = history.data?.pages.flatMap((page) => page.data) ?? [];

  return (
    <div className="stack">
      <PageHeader
        title="Requests"
        documentTitle={`Requests · ${p.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          { label: workspace.data?.name ?? "Workspace", to: "/workspaces/$workspaceId", params: { workspaceId: p.workspaceId } },
          { label: p.name, to: "/projects/$projectId", params: { projectId } },
          { label: "Requests" },
        ]}
      />
      <div className="requests">
        <section className="requests-history" aria-labelledby="history-title">
          <h2 id="history-title" className="section-title">
            Your history
          </h2>
          {history.isError ? <LoadError message={describeError(history.error)} onRetry={() => void history.refetch()} /> : null}
          {history.isSuccess && entries.length === 0 ? <p className="field-hint">Requests you send appear here for 30 days.</p> : null}
          <ul className="history-list">
            {entries.map((e) => (
              <li key={e.id}>
                <button type="button" className="history-item" onClick={() => open.mutate(e.id)} aria-current={shown?.id === e.id ? "true" : undefined}>
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
        <div className="requests-main">
          {!canSend && workspace.data ? (
            <p className="notice">Viewers can read their history but can't send requests. Ask a workspace owner or admin to make you an editor.</p>
          ) : null}
          {notice ? <p className="notice">{notice}</p> : null}
          <RequestEditor draft={draft} onChange={setDraft} onSend={() => send.mutate(draft)} sending={send.isPending} canSend={canSend} />
          {send.isError ? (
            <p className="form-alert" role="alert">
              {describeError(send.error)}
            </p>
          ) : null}
          {open.isError ? (
            <p className="form-alert" role="alert">
              {describeError(open.error)}
            </p>
          ) : null}
          {shown ? <ResponseView key={shown.id} entry={shown} /> : <p className="field-hint">Send a request to see its response here.</p>}
        </div>
      </div>
    </div>
  );
}
