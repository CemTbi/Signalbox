// SPDX-License-Identifier: Apache-2.0

import type { Run, RunStart } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useEffect, useId, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { fieldError, FormAlert } from "../../components/Form";
import { LoadError, Loading, PageHeader } from "../../components/Page";
import { collectionQuery, environmentsQuery, itemsQuery, projectQuery, runsQuery, workspaceQuery } from "../../queries";
import { RunOnSelect, useRunOn } from "../agents/RunOn";
import { folders, formatMs, rememberedEnvironment, rememberEnvironment } from "../requests/model";
import { dataFormat, maxDataBytes, statusLabel } from "./model";

const setupRoute = getRouteApi("/_app/collections/$collectionId/runs");

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" });

/** Starting a run of a collection or a folder, and the collection's past runs. */
export function RunSetupPage() {
  const { collectionId } = setupRoute.useParams();
  const { folder } = setupRoute.useSearch();
  const collection = useQuery(collectionQuery(api, collectionId));
  const projectId = collection.data?.projectId ?? "";
  const project = useQuery({ ...projectQuery(api, projectId), enabled: projectId !== "" });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const runs = useInfiniteQuery(runsQuery(api, collectionId));

  if (collection.isPending) return <Loading label="Loading collection…" />;
  if (collection.isError) return <LoadError message={describeError(collection.error)} onRetry={() => void collection.refetch()} />;
  const c = collection.data;
  const canRun = workspace.data !== undefined && workspace.data.role !== "viewer";
  const list = runs.data?.pages.flatMap((p) => p.data) ?? [];

  return (
    <div className="stack">
      <PageHeader
        title={`Runs of ${c.name}`}
        documentTitle={`Runs · ${c.name}`}
        crumbs={[
          { label: "Workspaces", to: "/workspaces" },
          ...(project.data
            ? [
                { label: project.data.name, to: "/projects/$projectId", params: { projectId } },
                { label: "Requests", to: "/projects/$projectId/requests", params: { projectId } },
              ]
            : []),
          { label: `Runs of ${c.name}` },
        ]}
        meta={<p className="lede">Run the collection, or a folder of it, as a test suite: every request in order, with its scripts and tests.</p>}
      />
      <div className="split">
        <section aria-labelledby="past-runs" className="split-main">
          <h2 id="past-runs" className="section-title">
            Past runs
          </h2>
          {runs.isError ? <LoadError message={describeError(runs.error)} onRetry={() => void runs.refetch()} /> : null}
          {runs.isPending ? <Loading label="Loading runs…" /> : null}
          {runs.isSuccess && list.length === 0 ? (
            <div className="empty-state">
              <p className="empty-title">No runs yet</p>
              <p>Runs are kept for 90 days, and the newest 100 of each collection.</p>
            </div>
          ) : null}
          {list.length > 0 ? (
            <ul className="rows">
              {list.map((r) => (
                <RunRow key={r.id} run={r} />
              ))}
            </ul>
          ) : null}
          {runs.hasNextPage ? (
            <button type="button" className="button secondary small" disabled={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()}>
              {runs.isFetchingNextPage ? "Loading…" : "Older runs"}
            </button>
          ) : null}
        </section>
        <section aria-labelledby="new-run" className="panel split-side">
          <h2 id="new-run" className="section-title">
            New run
          </h2>
          {workspace.data && !canRun ? (
            <p className="notice">Viewers can see runs and their reports, but can't start them. Ask a workspace owner or admin to make you an editor.</p>
          ) : projectId ? (
            <RunForm collectionId={collectionId} projectId={projectId} initialFolder={folder ?? ""} disabled={!canRun} />
          ) : null}
        </section>
      </div>
    </div>
  );
}

export function RunRow({ run: r }: { run: Run }) {
  return (
    <li>
      <Link className="row" to="/runs/$runId" params={{ runId: r.id }}>
        <span className="row-main">
          <span className="row-title">
            <span className={`run-status run-${r.status}`}>{statusLabel(r.status)}</span> {r.name}
          </span>
          <span className="row-sub">
            {timeFormat.format(new Date(r.createdAt))}
            {r.environment ? ` · ${r.environment}` : ""}
            {r.agent ? ` · on ${r.agent}` : ""} · {r.totals.requests} request{r.totals.requests === 1 ? "" : "s"}
            {r.totals.tests ? ` · ${r.totals.tests - r.totals.failedTests}/${r.totals.tests} tests passed` : ""}
            {r.durationMs ? ` · ${formatMs(r.durationMs)}` : ""}
          </span>
        </span>
        <span className="row-go" aria-hidden="true">
          →
        </span>
      </Link>
    </li>
  );
}

function RunForm({ collectionId, projectId, initialFolder, disabled }: { collectionId: string; projectId: string; initialFolder: string; disabled: boolean }) {
  const navigate = useNavigate();
  const items = useQuery(itemsQuery(api, collectionId));
  const environments = useQuery(environmentsQuery(api, projectId));
  const [folderId, setFolderId] = useState(initialFolder);
  const [environmentId, setEnvironmentId] = useState(() => rememberedEnvironment(projectId));
  const [iterations, setIterations] = useState("");
  const [delayMs, setDelayMs] = useState("0");
  const [bail, setBail] = useState(false);
  const [data, setData] = useState<{ name: string; content: string } | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const runOn = useRunOn();
  const ids = { folder: useId(), environment: useId(), data: useId(), dataHint: useId(), bail: useId() };
  const environment = environments.data?.data.find((e) => e.id === environmentId);
  useEffect(() => {
    // An environment that's gone isn't chosen.
    if (environments.isSuccess && environmentId !== "" && !environment) setEnvironmentId("");
  }, [environments.isSuccess, environmentId, environment]);

  const start = useMutation({
    mutationFn: (body: RunStart) => unwrap(api.POST("/api/v1/collections/{collectionId}/runs", { params: { path: { collectionId } }, body })),
    onSuccess: (run) => void navigate({ to: "/runs/$runId", params: { runId: run.id } }),
  });

  async function chooseFile(file: File | undefined) {
    setFileError(null);
    if (!file) {
      setData(null);
      return;
    }
    if (file.size > maxDataBytes) {
      setData(null);
      setFileError("The data file is larger than 5 MiB.");
      return;
    }
    setData({ name: file.name, content: await file.text() });
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const body: RunStart = { bail };
    if (folderId) body.folderId = folderId;
    if (environment) body.environmentId = environment.id;
    if (runOn.agent) body.agentId = runOn.agent.id;
    if (iterations.trim() !== "") body.iterations = Number(iterations);
    if (delayMs.trim() !== "") body.delayMs = Number(delayMs);
    if (data) body.data = { format: dataFormat(data.name), content: data.content };
    start.mutate(body);
  }

  const folderList = folders(items.data?.data ?? []);
  const dataError = fileError ?? fieldError(start.error, "data/content") ?? fieldError(start.error, "data/format");
  return (
    <form className="form" onSubmit={submit} noValidate>
      <FormAlert error={start.error} fields={["folderId", "environmentId", "agentId", "iterations", "data/content", "data/format", "delayMs"]} />
      <div className="field">
        <label htmlFor={ids.folder}>What to run</label>
        <select id={ids.folder} value={folderId} onChange={(e) => setFolderId(e.target.value)} disabled={disabled}>
          <option value="">The whole collection</option>
          {folderList.map(({ item, depth }) => (
            <option key={item.id} value={item.id}>
              {"  ".repeat(depth)}
              {item.name}
            </option>
          ))}
        </select>
        {fieldError(start.error, "folderId") ? <p className="field-error">{fieldError(start.error, "folderId")}</p> : null}
      </div>
      <div className="field">
        <label htmlFor={ids.environment}>Environment</label>
        <select
          id={ids.environment}
          value={environment?.id ?? ""}
          disabled={disabled}
          onChange={(e) => {
            setEnvironmentId(e.target.value);
            rememberEnvironment(projectId, e.target.value);
          }}
        >
          <option value="">No environment</option>
          {environments.data?.data.map((env) => (
            <option key={env.id} value={env.id}>
              {env.name}
            </option>
          ))}
        </select>
      </div>
      {runOn.agents.length > 0 ? (
        <div className="field">
          <RunOnSelect agent={runOn.agent} agents={runOn.agents} onChange={runOn.choose} />
        </div>
      ) : null}
      <div className="field">
        <label htmlFor={ids.data}>Data file</label>
        <input id={ids.data} type="file" accept=".csv,.json,text/csv,application/json" disabled={disabled} aria-describedby={ids.dataHint} onChange={(e) => void chooseFile(e.target.files?.[0])} />
        <p id={ids.dataHint} className="field-hint">
          Optional. CSV with a header row, or a JSON array of objects. Each row is one iteration, and fills {"{{name}}"} and pm.iterationData.
        </p>
        {dataError ? <p className="field-error">{dataError}</p> : null}
      </div>
      <FieldNumber
        label="Iterations"
        value={iterations}
        onChange={setIterations}
        min={1}
        max={1000}
        placeholder={data ? "One per data row" : "1"}
        hint="How many times the requests run. With a data file, leave it empty to run once per row."
        error={fieldError(start.error, "iterations")}
        disabled={disabled}
      />
      <FieldNumber
        label="Delay between requests (ms)"
        value={delayMs}
        onChange={setDelayMs}
        min={0}
        max={60000}
        placeholder="0"
        error={fieldError(start.error, "delayMs")}
        disabled={disabled}
      />
      <div className="checkbox-field">
        <input id={ids.bail} type="checkbox" checked={bail} disabled={disabled} onChange={(e) => setBail(e.target.checked)} />
        <label htmlFor={ids.bail}>Stop at the first failure</label>
      </div>
      <button type="submit" className="button primary" disabled={disabled || start.isPending}>
        {start.isPending ? "Starting…" : "Start run"}
      </button>
    </form>
  );
}

function FieldNumber(props: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  min: number;
  max: number;
  placeholder: string;
  hint?: string;
  error?: string;
  disabled: boolean;
}) {
  const id = useId();
  const describedBy = [props.hint ? `${id}-hint` : "", props.error ? `${id}-error` : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={id}>{props.label}</label>
      <input
        id={id}
        type="number"
        inputMode="numeric"
        min={props.min}
        max={props.max}
        step={1}
        value={props.value}
        placeholder={props.placeholder}
        disabled={props.disabled}
        aria-invalid={props.error ? true : undefined}
        aria-describedby={describedBy}
        onChange={(e) => props.onChange(e.target.value)}
      />
      {props.hint ? (
        <p id={`${id}-hint`} className="field-hint">
          {props.hint}
        </p>
      ) : null}
      {props.error ? (
        <p id={`${id}-error`} className="field-error">
          {props.error}
        </p>
      ) : null}
    </div>
  );
}
