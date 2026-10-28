// SPDX-License-Identifier: Apache-2.0

import type { Revision } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { api, describeError, unwrap, unwrapTagged } from "../../api";
import { type Crumb, LoadError, Loading, PageHeader } from "../../components/Page";
import {
  projectQuery,
  revisionContentQuery,
  revisionQuery,
  revisionReportQuery,
  revisionsKey,
  revisionsQuery,
  serviceQuery,
  specQuery,
  versionsKey,
  versionsQuery,
  workspaceQuery,
} from "../../queries";
import { summarize } from "../spec-check/lines";
import { CodeDownload } from "./CodeDownload";
import { ContractTests } from "./ContractTests";
import { SpecEditor } from "./SpecEditor";
import { type PublishValues, Versions } from "./Versions";

const specRoute = getRouteApi("/_app/specs/$specId");

/** One spec: its current document and problems, and its history. */
export function SpecPage() {
  const { specId } = specRoute.useParams();
  const queryClient = useQueryClient();
  const spec = useQuery(specQuery(api, specId));
  const current = spec.data?.data.currentRevisionId ?? "";
  const revision = useQuery({ ...revisionQuery(api, current), enabled: current !== "" });
  const content = useQuery({ ...revisionContentQuery(api, current), enabled: current !== "" });
  const report = useQuery({ ...revisionReportQuery(api, current), enabled: current !== "" });
  const history = useInfiniteQuery(revisionsQuery(api, specId));
  const versions = useInfiniteQuery(versionsQuery(api, specId));
  const publish = useMutation({
    mutationFn: ({ name, notes }: PublishValues) =>
      unwrap(
        api.POST("/api/v1/specs/{specId}/versions", {
          params: { path: { specId } },
          body: { ...(name ? { name } : {}), ...(notes ? { notes } : {}), ...(current ? { revisionId: current } : {}) },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: versionsKey(specId) }),
  });
  const service = useQuery({ ...serviceQuery(api, spec.data?.data.serviceId ?? ""), enabled: spec.isSuccess });
  const project = useQuery({ ...projectQuery(api, service.data?.projectId ?? ""), enabled: service.isSuccess });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });

  const save = useMutation({
    mutationFn: ({ text, message }: { text: string; message: string }) =>
      unwrapTagged(
        api.POST("/api/v1/specs/{specId}/revisions", {
          params: { path: { specId }, header: { "If-Match": spec.data?.etag ?? "" } },
          body: message ? { content: text, message } : { content: text },
        }),
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["specs", specId] });
      await queryClient.invalidateQueries({ queryKey: revisionsKey(specId) });
    },
  });

  if (spec.isPending) return <Loading label="Loading spec…" />;
  if (spec.isError) return <LoadError message={describeError(spec.error)} onRetry={() => void spec.refetch()} />;

  const s = spec.data.data;
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";
  const crumbs: Crumb[] = [
    { label: "Workspaces", to: "/workspaces" },
    ...(workspace.data ? [{ label: workspace.data.name, to: "/workspaces/$workspaceId", params: { workspaceId: workspace.data.id } }] : []),
    ...(project.data ? [{ label: project.data.name, to: "/projects/$projectId", params: { projectId: project.data.id } }] : []),
    ...(service.data ? [{ label: service.data.name, to: "/services/$serviceId", params: { serviceId: service.data.id } }] : []),
    { label: s.name },
  ];
  const r = revision.data;
  const revisions = history.data?.pages.flatMap((p) => p.data) ?? [];
  const published = versions.data?.pages.flatMap((p) => p.data) ?? [];

  return (
    <div className="stack">
      <PageHeader
        title={s.name}
        crumbs={crumbs}
        meta={
          r ? (
            <p className="lede">
              {r.title || "Untitled"} {r.apiVersion ? `· version ${r.apiVersion}` : ""} · revision {r.number}
            </p>
          ) : null
        }
      />
      {content.isError || report.isError ? (
        <LoadError
          message={describeError(content.error ?? report.error)}
          onRetry={() => {
            void content.refetch();
            void report.refetch();
          }}
        />
      ) : content.data !== undefined && report.data ? (
        <SpecEditor
          content={content.data}
          report={report.data}
          canEdit={canEdit}
          saving={save.isPending}
          error={save.error}
          onSave={(text, message) => save.mutate({ text, message })}
          onReload={() => {
            save.reset();
            void queryClient.invalidateQueries({ queryKey: ["specs", specId] });
          }}
        />
      ) : (
        <Loading label="Loading the document…" />
      )}
      <Versions
        versions={published}
        current={revision.data}
        canPublish={canEdit}
        pending={publish.isPending}
        error={publish.error}
        onPublish={(v) => publish.mutate(v)}
      />
      <CodeDownload client={api} versions={published} />
      {service.data ? <ContractTests versions={published} projectId={service.data.projectId} canEdit={canEdit} /> : null}
      <section aria-labelledby="history">
        <h2 id="history" className="section-title">
          History
        </h2>
        {history.isError ? <LoadError message={describeError(history.error)} onRetry={() => void history.refetch()} /> : null}
        <ol className="rows history">
          {revisions.map((rev) => (
            <HistoryRow key={rev.id} revision={rev} current={rev.id === s.currentRevisionId} />
          ))}
        </ol>
        {history.hasNextPage ? (
          <button type="button" className="button secondary" disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
            {history.isFetchingNextPage ? "Loading…" : "Show older revisions"}
          </button>
        ) : null}
      </section>
    </div>
  );
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function HistoryRow({ revision: r, current }: { revision: Revision; current: boolean }) {
  return (
    <li className="row">
      <span className="row-main">
        <span className="row-title">
          Revision {r.number}
          {current ? " (current)" : ""}
          {r.message ? ` · ${r.message}` : ""}
        </span>
        <span className="row-sub">
          {dateFormat.format(new Date(r.createdAt))} · {r.apiVersion ? `version ${r.apiVersion} · ` : ""}
          {summarize(r.counts)}
        </span>
      </span>
    </li>
  );
}
