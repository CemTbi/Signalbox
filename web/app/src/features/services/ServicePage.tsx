// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { api, describeError, unwrap } from "../../api";
import { type Crumb, LoadError, Loading, PageHeader } from "../../components/Page";
import { projectQuery, serviceQuery, specsKey, specsQuery, workspaceQuery } from "../../queries";
import { SpecForm, type SpecValues } from "./forms";

const serviceRoute = getRouteApi("/_app/services/$serviceId");

/** One service: where it sits, and its specs. */
export function ServicePage() {
  const { serviceId } = serviceRoute.useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const service = useQuery(serviceQuery(api, serviceId));
  const project = useQuery({ ...projectQuery(api, service.data?.projectId ?? ""), enabled: service.isSuccess });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });
  const specs = useInfiniteQuery(specsQuery(api, serviceId));
  const create = useMutation({
    mutationFn: (values: SpecValues) =>
      unwrap(api.POST("/api/v1/services/{serviceId}/specs", { params: { path: { serviceId } }, body: values })),
    onSuccess: async (spec) => {
      await queryClient.invalidateQueries({ queryKey: specsKey(serviceId) });
      await navigate({ to: "/specs/$specId", params: { specId: spec.id } });
    },
  });

  if (service.isPending) return <Loading label="Loading service…" />;
  if (service.isError) return <LoadError message={describeError(service.error)} onRetry={() => void service.refetch()} />;

  const s = service.data;
  const list = specs.data?.pages.flatMap((p) => p.data) ?? [];
  const canEdit = workspace.data !== undefined && workspace.data.role !== "viewer";
  const crumbs: Crumb[] = [
    { label: "Workspaces", to: "/workspaces" },
    ...(workspace.data ? [{ label: workspace.data.name, to: "/workspaces/$workspaceId", params: { workspaceId: workspace.data.id } }] : []),
    ...(project.data ? [{ label: project.data.name, to: "/projects/$projectId", params: { projectId: project.data.id } }] : []),
    { label: s.name },
  ];

  return (
    <div className="stack">
      <PageHeader title={s.name} crumbs={crumbs} meta={<p className="lede mono">{s.slug}</p>} />
      <div className="split">
        <section aria-labelledby="specs" className="split-main">
          <h2 id="specs" className="section-title">
            Specs
          </h2>
          {specs.isPending ? <Loading label="Loading specs…" /> : null}
          {specs.isError ? <LoadError message={describeError(specs.error)} onRetry={() => void specs.refetch()} /> : null}
          {specs.isSuccess && list.length === 0 ? (
            <div className="empty-state">
              <p className="empty-title">No spec yet</p>
              <p>Add the service's OpenAPI document. Signalbox checks it and keeps every version you save.</p>
            </div>
          ) : null}
          {list.length > 0 ? (
            <ul className="rows">
              {list.map((spec) => (
                <li key={spec.id}>
                  <Link to="/specs/$specId" params={{ specId: spec.id }} className="row">
                    <span className="row-main">
                      <span className="row-title">{spec.name}</span>
                    </span>
                    <span className="row-go" aria-hidden="true">
                      Open →
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : null}
        </section>
        {canEdit ? (
          <section aria-labelledby="new-spec" className="panel split-side">
            <h2 id="new-spec" className="section-title">
              Add a spec
            </h2>
            <SpecForm onSubmit={(v) => create.mutate(v)} pending={create.isPending} error={create.error} />
          </section>
        ) : null}
      </div>
    </div>
  );
}
