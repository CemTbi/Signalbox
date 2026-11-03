// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useId, useMemo, useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { canvasLiveQuery, dependenciesKey, dependenciesQuery, servicesKey, servicesQuery } from "../../queries";
import { slugify } from "../workspaces/slug";

/** Declares and removes dependencies, from the canvas or the list below it. */
export function useDependencyChanges(projectId: string) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: dependenciesKey(projectId) });
  const create = useMutation({
    mutationFn: (body: { fromServiceId: string; toServiceId: string }) =>
      unwrap(api.POST("/api/v1/projects/{projectId}/dependencies", { params: { path: { projectId } }, body })),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (dependencyId: string) => unwrap(api.DELETE("/api/v1/dependencies/{dependencyId}", { params: { path: { dependencyId } } })),
    onSuccess: refresh,
  });
  return { create, remove };
}

/**
 * Services sending spans that aren't on the canvas, with a button to add
 * them. Adding one tells it its telemetry name, so its spans match at once.
 */
export function SeenServices({ projectId, environmentId, canEdit }: { projectId: string; environmentId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const live = useQuery(canvasLiveQuery(api, projectId, environmentId));
  const services = useInfiniteQuery(servicesQuery(api, projectId));
  // The live answer is up to 5 seconds old, so a service just added may
  // still be listed as seen.
  const known = useMemo(() => {
    const names = new Set<string>();
    for (const s of services.data?.pages.flatMap((p) => p.data) ?? []) {
      names.add(s.telemetryServiceName ?? s.slug);
    }
    return names;
  }, [services.data]);
  const add = useMutation({
    mutationFn: (name: string) =>
      unwrap(
        api.POST("/api/v1/projects/{projectId}/services", {
          params: { path: { projectId } },
          body: { name: name.slice(0, 100), slug: slugify(name) || "service", telemetryServiceName: name },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: servicesKey(projectId) }),
  });
  const seen = (live.data?.services ?? []).filter((s) => !s.onCanvas && !known.has(s.name)).sort((a, b) => a.name.localeCompare(b.name));
  if (seen.length === 0 && !add.isError) return null;
  return (
    <section aria-labelledby="seen-services" className="stack-tight">
      <h2 id="seen-services" className="section-title">
        Seen in traces
      </h2>
      <p className="field-hint">These services send spans to this environment but aren't on the canvas.</p>
      {add.isError ? (
        <p className="form-alert" role="alert">
          The service couldn't be added: {describeError(add.error)}
        </p>
      ) : null}
      <ul className="rows">
        {seen.map((s) => (
          <li key={s.id} className="row">
            <span className="row-main">
              <span className="row-title mono">{s.name}</span>
              <span className="row-sub">{s.rate > 0 ? `${s.rate}/s` : "No recent requests"}</span>
            </span>
            {canEdit ? (
              <button type="button" className="button secondary small" disabled={add.isPending} onClick={() => add.mutate(s.name)} aria-label={`Add ${s.name} to the canvas`}>
                Add
              </button>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

/** The dependencies declared in a project, and a form to declare one, which needs no dragging. */
export function DeclaredDependencies({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  const dependencies = useQuery(dependenciesQuery(api, projectId));
  const services = useInfiniteQuery(servicesQuery(api, projectId));
  const list = useMemo(() => services.data?.pages.flatMap((p) => p.data) ?? [], [services.data]);
  const { create, remove } = useDependencyChanges(projectId);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const ids = { from: useId(), to: useId() };
  const nameOf = (id: string) => list.find((s) => s.id === id)?.name ?? id;
  const declared = dependencies.data?.data ?? [];
  if (list.length < 2 && declared.length === 0) return null;

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate({ fromServiceId: from, toServiceId: to }, { onSuccess: () => setTo("") });
  }
  return (
    <section aria-labelledby="dependencies" className="stack-tight">
      <h2 id="dependencies" className="section-title">
        Declared dependencies
      </h2>
      <p className="field-hint">
        Calls that traces don't show, dashed on the canvas until they do.
        {canEdit ? " Draw one from a service's right edge to another's left edge, or choose them here." : ""}
      </p>
      {dependencies.isError ? (
        <p className="form-alert" role="alert">
          The dependencies couldn't load: {describeError(dependencies.error)}
        </p>
      ) : null}
      {declared.length > 0 ? (
        <ul className="rows">
          {declared.map((d) => (
            <li key={d.id} className="row">
              <span className="row-main">
                <span className="row-title">
                  {nameOf(d.fromServiceId)} → {nameOf(d.toServiceId)}
                </span>
              </span>
              {canEdit ? (
                <button
                  type="button"
                  className="button ghost small"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(d.id)}
                  aria-label={`Remove the dependency from ${nameOf(d.fromServiceId)} to ${nameOf(d.toServiceId)}`}
                >
                  Remove
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      {remove.isError ? (
        <p className="form-alert" role="alert">
          The dependency couldn't be removed: {describeError(remove.error)}
        </p>
      ) : null}
      {canEdit && list.length >= 2 ? (
        <form className="dependency-form" onSubmit={submit} aria-label="Declare a dependency">
          <div className="field">
            <label htmlFor={ids.from}>From</label>
            <select id={ids.from} value={from} required onChange={(e) => setFrom(e.target.value)}>
              <option value="">Choose a service</option>
              {list.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor={ids.to}>To</label>
            <select id={ids.to} value={to} required onChange={(e) => setTo(e.target.value)}>
              <option value="">Choose a service</option>
              {list
                .filter((s) => s.id !== from)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
            </select>
          </div>
          <button type="submit" className="button secondary" disabled={create.isPending}>
            Declare dependency
          </button>
        </form>
      ) : null}
      {create.isError ? (
        <p className="form-alert" role="alert">
          The dependency couldn't be declared: {describeError(create.error)}
        </p>
      ) : null}
    </section>
  );
}
