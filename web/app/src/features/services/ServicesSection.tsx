// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading } from "../../components/Page";
import { servicesKey, servicesQuery } from "../../queries";
import { ServiceForm, type ServiceValues } from "./forms";

/** A project's services, and a form to add one for those who may. */
export function ServicesSection({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const services = useInfiniteQuery(servicesQuery(api, projectId));
  const create = useMutation({
    mutationFn: (values: ServiceValues) =>
      unwrap(api.POST("/api/v1/projects/{projectId}/services", { params: { path: { projectId } }, body: values })),
    onSuccess: async (service) => {
      await queryClient.invalidateQueries({ queryKey: servicesKey(projectId) });
      await navigate({ to: "/services/$serviceId", params: { serviceId: service.id } });
    },
  });
  const list = services.data?.pages.flatMap((p) => p.data) ?? [];

  return (
    <div className="split">
      <section aria-labelledby="services" className="split-main">
        <h2 id="services" className="section-title">
          Services
        </h2>
        {services.isPending ? <Loading label="Loading services…" /> : null}
        {services.isError ? <LoadError message={describeError(services.error)} onRetry={() => void services.refetch()} /> : null}
        {services.isSuccess && list.length === 0 ? (
          <div className="empty-state">
            <p className="empty-title">No services yet</p>
            <p>A service is one API of this system. Add it, then give it its OpenAPI document.</p>
          </div>
        ) : null}
        {list.length > 0 ? (
          <ul className="rows">
            {list.map((s) => (
              <li key={s.id}>
                <Link to="/services/$serviceId" params={{ serviceId: s.id }} className="row">
                  <span className="row-main">
                    <span className="row-title">{s.name}</span>
                    <span className="row-sub mono">{s.slug}</span>
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
        <section aria-labelledby="new-service" className="panel split-side">
          <h2 id="new-service" className="section-title">
            Add a service
          </h2>
          <ServiceForm onSubmit={(v) => create.mutate(v)} pending={create.isPending} error={create.error} />
        </section>
      ) : null}
    </div>
  );
}
