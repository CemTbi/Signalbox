// SPDX-License-Identifier: Apache-2.0

import type { Reference, ReferenceGroup, Version } from "@signalbox/api-client";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { api, describeError } from "../../api";
import { type Crumb, LoadError, Loading, PageHeader } from "../../components/Page";
import { projectQuery, referenceQuery, serviceQuery, specQuery, versionsQuery, workspaceQuery } from "../../queries";
import { mockURL } from "../services/Versions";
import { Markdown } from "./Markdown";
import { Operation, operationAnchor, type Target } from "./Operation";
import { type Language, languages } from "./samples";
import { SchemaBody, schemaAnchor } from "./SchemaView";

const docsRoute = getRouteApi("/_app/specs/$specId/docs");

const LANGUAGE_KEY = "signalbox.docs.language";

function storedLanguage(): Language {
  try {
    const v = localStorage.getItem(LANGUAGE_KEY);
    if (languages.some((l) => l.id === v)) return v as Language;
  } catch {
    // Storage can be unavailable, such as in private windows.
  }
  return "curl";
}

/** A published version's reference docs, with a switcher between versions. */
export function DocsPage() {
  const { specId } = docsRoute.useParams();
  const { version: wanted } = docsRoute.useSearch();
  const navigate = useNavigate({ from: "/specs/$specId/docs" });
  const spec = useQuery(specQuery(api, specId));
  const versions = useInfiniteQuery(versionsQuery(api, specId));
  const all = versions.data?.pages.flatMap((p) => p.data) ?? [];
  const version = wanted ? all.find((v) => v.name === wanted) : all[0];
  // A version named in the address may be on a later page.
  useEffect(() => {
    if (wanted && !version && versions.hasNextPage && !versions.isFetchingNextPage) void versions.fetchNextPage();
  }, [wanted, version, versions]);
  const reference = useQuery({ ...referenceQuery(api, version?.id ?? ""), enabled: version !== undefined });
  const service = useQuery({ ...serviceQuery(api, spec.data?.data.serviceId ?? ""), enabled: spec.isSuccess });
  const project = useQuery({ ...projectQuery(api, service.data?.projectId ?? ""), enabled: service.isSuccess });
  const workspace = useQuery({ ...workspaceQuery(api, project.data?.workspaceId ?? ""), enabled: project.isSuccess });

  const s = spec.data?.data;
  const crumbs: Crumb[] = [
    { label: "Workspaces", to: "/workspaces" },
    ...(workspace.data ? [{ label: workspace.data.name, to: "/workspaces/$workspaceId", params: { workspaceId: workspace.data.id } }] : []),
    ...(project.data ? [{ label: project.data.name, to: "/projects/$projectId", params: { projectId: project.data.id } }] : []),
    ...(service.data ? [{ label: service.data.name, to: "/services/$serviceId", params: { serviceId: service.data.id } }] : []),
    ...(s ? [{ label: s.name, to: "/specs/$specId", params: { specId } }] : []),
    { label: "Docs" },
  ];

  if (spec.isError) return <LoadError message={describeError(spec.error)} onRetry={() => void spec.refetch()} />;
  if (versions.isError) return <LoadError message={describeError(versions.error)} onRetry={() => void versions.refetch()} />;
  if (spec.isPending || versions.isPending) return <Loading label="Loading docs…" />;

  const title = reference.data?.title || s?.name || "Docs";
  const header = (
    <PageHeader
      title={title}
      documentTitle={`${title} docs`}
      crumbs={crumbs}
      meta={all.length > 0 ? <VersionPicker versions={all} current={version} onChange={(name) => void navigate({ search: { version: name } })} /> : null}
    />
  );

  if (all.length === 0) {
    return (
      <div className="stack">
        {header}
        <div className="empty-state">
          <p className="empty-title">Nothing published yet</p>
          <p>
            Docs show published versions. <Link to="/specs/$specId" params={{ specId }}>Publish a version</Link> of this spec to see them.
          </p>
        </div>
      </div>
    );
  }
  if (!version) {
    return (
      <div className="stack">
        {header}
        {versions.hasNextPage || versions.isFetchingNextPage ? (
          <Loading label="Finding the version…" />
        ) : (
          <div className="empty-state">
            <p className="empty-title">There's no version {wanted}</p>
            <p>
              <Link to="/specs/$specId/docs" params={{ specId }} search={{}}>
                Read the latest version
              </Link>
            </p>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="stack">
      {header}
      {reference.isError ? (
        <LoadError message={describeError(reference.error)} onRetry={() => void reference.refetch()} />
      ) : reference.isPending ? (
        <Loading label="Loading the reference…" />
      ) : (
        <ReferenceView reference={reference.data} specId={specId} version={version} />
      )}
    </div>
  );
}

function VersionPicker({ versions, current, onChange }: { versions: Version[]; current: Version | undefined; onChange: (name: string) => void }) {
  const id = useId();
  return (
    <div className="docs-version">
      <label htmlFor={id} className="field-label">
        Version
      </label>
      <select id={id} value={current?.name ?? ""} onChange={(e) => onChange(e.target.value)}>
        {versions.map((v, i) => (
          <option key={v.id} value={v.name}>
            {v.name}
            {i === 0 ? " (latest)" : ""}
          </option>
        ))}
      </select>
    </div>
  );
}

/** Groups nested in other groups (OpenAPI 3.2) follow their parent. */
function ordered(groups: ReferenceGroup[]) {
  const names = new Set(groups.map((g) => g.name));
  const out: { group: ReferenceGroup; index: number; depth: number }[] = [];
  const visit = (parent: string | undefined, depth: number) => {
    groups.forEach((g, index) => {
      const p = g.parent && names.has(g.parent) && g.parent !== g.name ? g.parent : undefined;
      if (p === parent && !out.some((o) => o.index === index)) {
        out.push({ group: g, index, depth });
        if (g.name) visit(g.name, depth + 1);
      }
    });
  };
  visit(undefined, 0);
  // Groups whose parents form a cycle still appear.
  groups.forEach((g, index) => {
    if (!out.some((o) => o.index === index)) out.push({ group: g, index, depth: 0 });
  });
  return out;
}

function groupTitle(g: ReferenceGroup) {
  return g.name === "" ? "Other operations" : g.summary || g.name;
}

function ReferenceView({ reference: ref, specId, version }: { reference: Reference; specId: string; version: Version }) {
  const mock = mockURL(specId, version.name);
  const servers = [{ url: mock, description: "Mock of this version" }, ...(ref.servers ?? [])];
  const [server, setServer] = useState(0);
  const [language, setLanguage] = useState<Language>(storedLanguage);
  const serverId = useId();
  const groups = ordered(ref.groups);
  // Links to a schema open its folded section.
  useEffect(() => {
    const open = () => {
      const target = document.getElementById(decodeURIComponent(window.location.hash.slice(1)));
      if (target instanceof HTMLDetailsElement && !target.open) {
        target.open = true;
        target.scrollIntoView();
      }
    };
    open();
    window.addEventListener("hashchange", open);
    return () => window.removeEventListener("hashchange", open);
  }, []);
  const target: Target = {
    base: servers[server]?.url ?? mock,
    mock,
    schemes: ref.securitySchemes ?? [],
    language,
    onLanguage: (l) => {
      setLanguage(l);
      try {
        localStorage.setItem(LANGUAGE_KEY, l);
      } catch {
        // Remembering the language is a convenience.
      }
    },
  };

  return (
    <div className="docs">
      <nav className="docs-nav" aria-label="Reference">
        <ul>
          <li>
            <a href="#overview">Overview</a>
          </li>
          {groups.map(({ group, index, depth }) => (
            <li key={index} className={depth > 0 ? "nested" : undefined}>
              <a href={`#group-${index}`} className="docs-nav-group">
                {groupTitle(group)}
              </a>
              <ul>
                {group.operations.map((op) => (
                  <li key={op.id}>
                    <a href={`#${operationAnchor(index, op)}`}>
                      <span className={`method method-${op.method.toLowerCase()}`}>{op.method}</span> {op.summary || op.path}
                    </a>
                  </li>
                ))}
              </ul>
            </li>
          ))}
          {ref.schemas && ref.schemas.length > 0 ? (
            <li>
              <a href="#schemas" className="docs-nav-group">
                Schemas
              </a>
            </li>
          ) : null}
        </ul>
      </nav>
      <div className="docs-main">
        <section id="overview" aria-labelledby="overview-title" className="docs-overview">
          <h2 id="overview-title" className="section-title">
            Overview
          </h2>
          <p className="lede">
            {ref.version ? `API version ${ref.version}` : null}
            {version.notes ? ` · ${version.notes}` : null}
          </p>
          <Markdown source={ref.summary} />
          <Markdown source={ref.description} />
          <div className="docs-server">
            <label htmlFor={serverId} className="field-label">
              Code samples call
            </label>
            <select id={serverId} value={server} onChange={(e) => setServer(Number(e.target.value))}>
              {servers.map((s, i) => (
                <option key={`${i}-${s.url}`} value={i}>
                  {s.description ? `${s.description}: ${s.url}` : s.url}
                </option>
              ))}
            </select>
          </div>
          {ref.securitySchemes && ref.securitySchemes.length > 0 ? (
            <div id="authorization">
              <h3 className="op-title">Authorization</h3>
              <ul className="props">
                {ref.securitySchemes.map((s) => (
                  <li key={s.name} className="prop">
                    <div className="prop-head">
                      <code className="prop-name">{s.name}</code>
                      <span className="type-name">{schemeLabel(s)}</span>
                    </div>
                    <Markdown source={s.description} />
                    {s.flows?.map((f) => (
                      <p key={f.type} className="schema-line">
                        OAuth 2 {f.type}
                        {f.authorizationUrl ? ` · authorize at ${f.authorizationUrl}` : ""}
                        {f.tokenUrl ? ` · tokens from ${f.tokenUrl}` : ""}
                        {f.scopes?.length ? ` · scopes ${f.scopes.map((x) => x.name).join(", ")}` : ""}
                      </p>
                    ))}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </section>
        {groups.map(({ group, index }) => (
          <section key={index} id={`group-${index}`} aria-labelledby={`group-${index}-title`} className="docs-group">
            <h2 id={`group-${index}-title`} className="section-title">
              {groupTitle(group)}
            </h2>
            <Markdown source={group.description} />
            {group.operations.map((op) => (
              <Operation key={op.id} op={op} anchor={operationAnchor(index, op)} target={target} />
            ))}
          </section>
        ))}
        {ref.schemas && ref.schemas.length > 0 ? (
          <section id="schemas" aria-labelledby="schemas-title" className="docs-group">
            <h2 id="schemas-title" className="section-title">
              Schemas
            </h2>
            {ref.schemas.map((s) => (
              <details key={s.name} id={schemaAnchor(s.name)} className="schema-section">
                <summary>
                  <code>{s.name}</code>
                </summary>
                <SchemaBody schema={s.schema} />
              </details>
            ))}
          </section>
        ) : null}
      </div>
    </div>
  );
}

function schemeLabel(s: NonNullable<Reference["securitySchemes"]>[number]) {
  switch (s.type) {
    case "http":
      return `HTTP ${s.scheme ?? ""}${s.bearerFormat ? ` (${s.bearerFormat})` : ""}`;
    case "apiKey":
      return `API key in ${s.in === "query" ? "query parameter" : (s.in ?? "header")} ${s.parameterName ?? ""}`;
    case "oauth2":
      return "OAuth 2";
    case "openIdConnect":
      return "OpenID Connect";
    default:
      return s.type;
  }
}
