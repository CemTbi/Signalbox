// SPDX-License-Identifier: Apache-2.0

import type { Run, RunStep } from "@signalbox/api-client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { collectionQuery, projectQuery, runKey, runQuery, running, runsKey, workspaceQuery } from "../../queries";
import { contentType, formatMs, formatSize, prettyBody } from "../requests/model";
import { ConsoleList, OpenTrace, ScriptErrors, TestList } from "../requests/ResponseView";
import { appendSteps, reportUrl, statusLabel, stepState, stepTests, stoppedReason } from "./model";

const runRoute = getRouteApi("/_app/runs/$runId");

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" });

/** A test run: its totals, its steps as they come, and its reports. */
export function RunPage() {
  const { runId } = runRoute.useParams();
  const queryClient = useQueryClient();
  const run = useQuery(runQuery(api, runId));
  const collectionId = run.data?.collectionId ?? "";
  const collection = useQuery({ ...collectionQuery(api, collectionId), enabled: collectionId !== "" });
  const projectId = run.data?.projectId ?? "";
  const project = useQuery({ ...projectQuery(api, projectId), enabled: projectId !== "" });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const steps = useRunSteps(runId, run.data ? !running(run.data.status) : false);
  const cancel = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/runs/{runId}/cancel", { params: { path: { runId } } })),
    onSuccess: async (r) => {
      queryClient.setQueryData(runKey(runId), r);
      await queryClient.invalidateQueries({ queryKey: runsKey(r.collectionId) });
    },
  });

  if (run.isPending) return <Loading label="Loading run…" />;
  if (run.isError) return <LoadError message={describeError(run.error)} onRetry={() => void run.refetch()} />;
  const r = run.data;
  const canRun = workspace.data !== undefined && workspace.data.role !== "viewer";
  const live = running(r.status);

  return (
    <div className="stack">
      <PageHeader
        title={r.name}
        documentTitle={`${statusLabel(r.status)} · ${r.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          ...(project.data ? [{ label: project.data.name, to: "/projects/$projectId", params: { projectId } }] : []),
          { label: collection.data ? `Runs of ${collection.data.name}` : "Runs", to: "/collections/$collectionId/runs", params: { collectionId } },
          { label: r.name },
        ]}
        meta={<RunMeta run={r} />}
        actions={
          <div className="row-actions">
            {live && canRun ? (
              <button type="button" className="button secondary" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
                {cancel.isPending ? "Cancelling…" : "Cancel run"}
              </button>
            ) : null}
            {!live && canRun ? (
              <Link className="button secondary" to="/collections/$collectionId/runs" params={{ collectionId }} search={r.folderId ? { folder: r.folderId } : {}}>
                Run again
              </Link>
            ) : null}
            <Reports runId={r.id} />
          </div>
        }
      />
      {cancel.isError ? (
        <p className="form-alert" role="alert">
          The run couldn't be cancelled: {describeError(cancel.error)}
        </p>
      ) : null}
      <RunSummary run={r} />
      {steps.error ? <LoadError message={describeError(steps.error)} /> : null}
      <StepList runId={r.id} projectId={r.projectId} steps={steps.steps} live={live} />
    </div>
  );
}

function RunMeta({ run: r }: { run: Run }) {
  const reason = stoppedReason(r.stopped);
  return (
    <>
      <p className="page-meta-line">
        <span className={`run-status run-${r.status}`} role="status">
          <span className="visually-hidden">Run status: </span>
          {statusLabel(r.status)}
        </span>{" "}
        {r.startedAt ? `Started ${timeFormat.format(new Date(r.startedAt))}` : `Queued ${timeFormat.format(new Date(r.createdAt))}`}
        {r.environment ? ` · ${r.environment}` : " · No environment"}
        {r.agent ? ` · on ${r.agent}` : ""} · {r.iterations} iteration{r.iterations === 1 ? "" : "s"}
        {r.dataRows ? ` · ${r.dataRows} data row${r.dataRows === 1 ? "" : "s"}` : ""}
        {r.startedAt ? ` · ${formatMs(r.durationMs)}` : ""}
      </p>
      {reason ? <p className="page-meta-line">{reason}</p> : null}
    </>
  );
}

function Reports({ runId }: { runId: string }) {
  return (
    <>
      <a className="button ghost" href={reportUrl(runId, "html")} download>
        HTML report
      </a>
      <a className="button ghost" href={reportUrl(runId, "junit")} download>
        JUnit
      </a>
      <a className="button ghost" href={reportUrl(runId, "json")} download>
        JSON
      </a>
    </>
  );
}

/** A run's totals. */
export function RunSummary({ run: r }: { run: Run }) {
  const t = r.totals;
  return (
    <dl className="run-totals" aria-label="Totals">
      <div>
        <dt>Requests</dt>
        <dd>{t.requests}</dd>
      </div>
      <div className={t.failedRequests ? "total-bad" : ""}>
        <dt>Failed requests</dt>
        <dd>{t.failedRequests}</dd>
      </div>
      <div>
        <dt>Tests</dt>
        <dd>{t.tests}</dd>
      </div>
      <div className={t.failedTests ? "total-bad" : ""}>
        <dt>Failed tests</dt>
        <dd>{t.failedTests}</dd>
      </div>
    </dl>
  );
}

/**
 * Follows a run's steps: asks for those after the last one seen, at once
 * while there are more, and every second while the run goes on.
 */
function useRunSteps(runId: string, finished: boolean) {
  const [steps, setSteps] = useState<RunStep[]>([]);
  const last = steps.at(-1)?.seq ?? 0;
  const page = useQuery({
    // finished is in the key, so the steps are asked for once more when the run ends.
    queryKey: ["runs", runId, "steps", last, finished],
    queryFn: () => unwrap(api.GET("/api/v1/runs/{runId}/steps", { params: { path: { runId }, query: { after: last, limit: 500 } } })),
    refetchInterval: finished ? false : 1000,
  });
  useEffect(() => {
    if (page.data) setSteps((shown) => appendSteps(shown, page.data.data));
  }, [page.data]);
  return { steps, error: page.error };
}

/** A run's steps, in order, each opening to its details. */
export function StepList({ runId, projectId, steps, live }: { runId: string; projectId?: string; steps: RunStep[]; live: boolean }) {
  const [failedOnly, setFailedOnly] = useState(false);
  const filterId = useId();
  const shown = failedOnly ? steps.filter((s) => s.failed) : steps;
  const iterations = new Set(steps.map((s) => s.iteration)).size;
  return (
    <section aria-labelledby={`${filterId}-title`} className="stack-tight">
      <div className="steps-head">
        <h2 id={`${filterId}-title`} className="section-title">
          Requests
        </h2>
        <span className="checkbox-field">
          <input id={filterId} type="checkbox" checked={failedOnly} onChange={(e) => setFailedOnly(e.target.checked)} />
          <label htmlFor={filterId}>Failed only</label>
        </span>
      </div>
      {steps.length === 0 ? <p className="field-hint">{live ? "Waiting for the first request…" : "The run sent no requests."}</p> : null}
      {steps.length > 0 && shown.length === 0 ? <p className="field-hint">No request failed.</p> : null}
      <ol className="steps" aria-label="Requests of the run">
        {shown.map((s) => (
          <StepRow key={s.seq} runId={runId} projectId={projectId} step={s} showIteration={iterations > 1} />
        ))}
      </ol>
      {live && steps.length > 0 ? <p className="field-hint">More requests appear as the run goes on.</p> : null}
    </section>
  );
}

function StepRow({ runId, projectId, step: s, showIteration }: { runId: string; projectId?: string; step: RunStep; showIteration: boolean }) {
  const [open, setOpen] = useState(false);
  const detailId = useId();
  const state = stepState(s);
  const tests = stepTests(s);
  return (
    <li className={`step step-${state}`}>
      <button type="button" className="step-row" aria-expanded={open} aria-controls={detailId} onClick={() => setOpen(!open)}>
        <span className={`run-status run-${state}`}>{state === "passed" ? "Passed" : state === "failed" ? "Failed" : "Skipped"}</span>
        <span className={`method method-${s.method.toLowerCase()}`}>{s.method}</span>
        <span className="step-name">
          {s.path ? <span className="muted">{s.path} / </span> : null}
          {s.name}
        </span>
        <span className="step-meta">
          {showIteration ? `#${s.iteration + 1} · ` : ""}
          {s.response ? <span className={`status status-${String(s.response.status).charAt(0)}`}>{s.response.status}</span> : null}
          {tests.total ? ` ${tests.passed}/${tests.total} tests` : ""} · {formatMs(s.durationMs)}
        </span>
      </button>
      {open ? (
        <div id={detailId} className="step-detail">
          <StepDetail runId={runId} seq={s.seq} projectId={projectId} />
        </div>
      ) : null}
    </li>
  );
}

function StepDetail({ runId, seq, projectId }: { runId: string; seq: number; projectId?: string }) {
  const step = useQuery({
    queryKey: ["runs", runId, "steps", "detail", seq],
    queryFn: () => unwrap(api.GET("/api/v1/runs/{runId}/steps/{seq}", { params: { path: { runId, seq } } })),
    staleTime: Number.POSITIVE_INFINITY,
  });
  if (step.isPending) return <Loading label="Loading the request…" />;
  if (step.isError) return <LoadError message={describeError(step.error)} onRetry={() => void step.refetch()} />;
  return <StepView step={step.data} projectId={projectId} />;
}

/** One step in full: what was sent, what came back, and what the scripts did. */
export function StepView({ step: s, projectId }: { step: RunStep; projectId?: string }) {
  const [tab, setTab] = useState<"tests" | "console" | "response" | "request">(s.scripts?.tests.length ? "tests" : "response");
  const r = s.response;
  return (
    <div className="stack-tight">
      <p className="mono step-url">
        {s.method} {s.url} <OpenTrace projectId={projectId} traceId={s.traceId} />
      </p>
      {s.failure ? (
        <p className={s.failure.code === "skipped" ? "notice" : "response-failure"}>
          <strong>{s.failure.code === "skipped" ? "Not sent." : "No response."}</strong> {s.failure.message}
        </p>
      ) : null}
      {s.note ? <p className="notice">{s.note}</p> : null}
      {s.unresolved?.length ? <p className="notice">No value for {s.unresolved.map((n) => `{{${n}}}`).join(", ")}, so it was sent as typed.</p> : null}
      {s.scripts?.errors.length ? <ScriptErrors errors={s.scripts.errors} /> : null}
      <div className="tabs" role="tablist" aria-label="Parts of the request">
        {s.scripts ? (
          <>
            <button type="button" role="tab" aria-selected={tab === "tests"} onClick={() => setTab("tests")}>
              Tests ({s.scripts.tests.filter((t) => t.passed).length}/{s.scripts.tests.length})
            </button>
            <button type="button" role="tab" aria-selected={tab === "console"} onClick={() => setTab("console")}>
              Console ({s.scripts.logs.length})
            </button>
          </>
        ) : null}
        <button type="button" role="tab" aria-selected={tab === "response"} onClick={() => setTab("response")}>
          Response
        </button>
        <button type="button" role="tab" aria-selected={tab === "request"} onClick={() => setTab("request")}>
          Request headers
        </button>
      </div>
      {tab === "tests" && s.scripts ? <TestList scripts={s.scripts} /> : null}
      {tab === "console" && s.scripts ? <ConsoleList scripts={s.scripts} /> : null}
      {tab === "response" ? (
        <div role="tabpanel">
          {r ? (
            <>
              <p className="response-line">
                <span className={`status status-${String(r.status).charAt(0)}`}>{r.status}</span> {r.statusText}
                <span className="muted">
                  {" "}
                  · {formatMs(s.durationMs)} · {formatSize(r.size)}
                </span>
              </p>
              {r.body ? (
                <pre className="code" tabIndex={0}>
                  <code>{prettyBody(r.body, contentType(r.headers ?? []))}</code>
                </pre>
              ) : (
                <p className="field-hint">The body is kept only for requests that failed.</p>
              )}
              <Headers headers={r.headers ?? []} label="Response headers" />
            </>
          ) : (
            <p className="field-hint">No response.</p>
          )}
        </div>
      ) : null}
      {tab === "request" ? (
        <div role="tabpanel">
          <Headers headers={s.headers ?? []} label="Request headers" />
        </div>
      ) : null}
    </div>
  );
}

function Headers({ headers, label }: { headers: { key: string; value: string }[]; label: string }) {
  if (headers.length === 0) return <p className="field-hint">No headers.</p>;
  return (
    <table className="kv" aria-label={label}>
      <tbody>
        {headers.map((h, i) => (
          <tr key={`${h.key}-${i}`}>
            <th scope="row" className="mono">
              {h.key}
            </th>
            <td className="mono">{h.value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
