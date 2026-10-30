// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from "@signalbox/api-client";
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { unwrap, unwrapTagged } from "./api";

const PAGE_SIZE = 50;

export const workspacesKey = ["workspaces"] as const;

/** The workspaces the user belongs to, a page at a time. */
export function workspacesQuery(client: ApiClient) {
  return infiniteQueryOptions({
    queryKey: workspacesKey,
    queryFn: ({ pageParam }) =>
      unwrap(client.GET("/api/v1/workspaces", { params: { query: { limit: PAGE_SIZE, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export function workspaceQuery(client: ApiClient, workspaceId: string) {
  return queryOptions({
    queryKey: [...workspacesKey, workspaceId],
    queryFn: () => unwrap(client.GET("/api/v1/workspaces/{workspaceId}", { params: { path: { workspaceId } } })),
  });
}

export const projectsKey = (workspaceId: string) => ["workspaces", workspaceId, "projects"] as const;

/** A workspace's projects, a page at a time. */
export function projectsQuery(client: ApiClient, workspaceId: string) {
  return infiniteQueryOptions({
    queryKey: projectsKey(workspaceId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/workspaces/{workspaceId}/projects", {
          params: { path: { workspaceId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export const membersKey = (workspaceId: string) => ["workspaces", workspaceId, "members"] as const;

/** A workspace's members, a page at a time. */
export function membersQuery(client: ApiClient, workspaceId: string) {
  return infiniteQueryOptions({
    queryKey: membersKey(workspaceId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/workspaces/{workspaceId}/members", {
          params: { path: { workspaceId }, query: { limit: 200, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export const invitationsKey = (workspaceId: string) => ["workspaces", workspaceId, "invitations"] as const;

/** A workspace's open invitations, a page at a time. Owners and admins only. */
export function invitationsQuery(client: ApiClient, workspaceId: string) {
  return infiniteQueryOptions({
    queryKey: invitationsKey(workspaceId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/workspaces/{workspaceId}/invitations", {
          params: { path: { workspaceId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export function projectQuery(client: ApiClient, projectId: string) {
  return queryOptions({
    queryKey: ["projects", projectId],
    queryFn: () => unwrap(client.GET("/api/v1/projects/{projectId}", { params: { path: { projectId } } })),
  });
}

export const servicesKey = (projectId: string) => ["projects", projectId, "services"] as const;

/** A project's services, oldest first, a page at a time. */
export function servicesQuery(client: ApiClient, projectId: string) {
  return infiniteQueryOptions({
    queryKey: servicesKey(projectId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/projects/{projectId}/services", {
          params: { path: { projectId }, query: { limit: 200, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export function serviceQuery(client: ApiClient, serviceId: string) {
  return queryOptions({
    queryKey: ["services", serviceId],
    queryFn: () => unwrap(client.GET("/api/v1/services/{serviceId}", { params: { path: { serviceId } } })),
  });
}

export const specsKey = (serviceId: string) => ["services", serviceId, "specs"] as const;

/** A service's specs, oldest first. */
export function specsQuery(client: ApiClient, serviceId: string) {
  return infiniteQueryOptions({
    queryKey: specsKey(serviceId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/services/{serviceId}/specs", {
          params: { path: { serviceId }, query: { limit: 200, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

/** A spec with its ETag, which a save must send back. */
export function specQuery(client: ApiClient, specId: string) {
  return queryOptions({
    queryKey: ["specs", specId],
    queryFn: () => unwrapTagged(client.GET("/api/v1/specs/{specId}", { params: { path: { specId } } })),
  });
}

export const revisionsKey = (specId: string) => ["specs", specId, "revisions"] as const;

/** A spec's revisions, newest first, a page at a time. */
export function revisionsQuery(client: ApiClient, specId: string) {
  return infiniteQueryOptions({
    queryKey: revisionsKey(specId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/specs/{specId}/revisions", {
          params: { path: { specId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

/** A revision's details. Revisions never change, so they never go stale. */
export function revisionQuery(client: ApiClient, revisionId: string) {
  return queryOptions({
    queryKey: ["revisions", revisionId],
    queryFn: () => unwrap(client.GET("/api/v1/revisions/{revisionId}", { params: { path: { revisionId } } })),
    staleTime: Number.POSITIVE_INFINITY,
  });
}

/** A revision's document, as text. */
export function revisionContentQuery(client: ApiClient, revisionId: string) {
  return queryOptions({
    queryKey: ["revisions", revisionId, "content"],
    queryFn: () =>
      unwrap(
        client.GET("/api/v1/revisions/{revisionId}/content", {
          params: { path: { revisionId } },
          parseAs: "text",
        }),
      ) as Promise<string>,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

/** The check report saved with a revision. */
export function revisionReportQuery(client: ApiClient, revisionId: string) {
  return queryOptions({
    queryKey: ["revisions", revisionId, "report"],
    queryFn: () => unwrap(client.GET("/api/v1/revisions/{revisionId}/report", { params: { path: { revisionId } } })),
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export const versionsKey = (specId: string) => ["specs", specId, "versions"] as const;

/** A spec's published versions, newest first. */
export function versionsQuery(client: ApiClient, specId: string) {
  return infiniteQueryOptions({
    queryKey: versionsKey(specId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/specs/{specId}/versions", {
          params: { path: { specId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

/** A published version's reference docs. Versions never change. */
export function referenceQuery(client: ApiClient, versionId: string) {
  return queryOptions({
    queryKey: ["versions", versionId, "reference"],
    queryFn: () => unwrap(client.GET("/api/v1/versions/{versionId}/reference", { params: { path: { versionId } } })),
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export const historyKey = (projectId: string) => ["projects", projectId, "history"] as const;

/** The requests the person sent from a project, newest first. */
export function historyQuery(client: ApiClient, projectId: string) {
  return infiniteQueryOptions({
    queryKey: historyKey(projectId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/projects/{projectId}/history", {
          params: { path: { projectId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

/** One sent request, as history keeps it. Entries never change. */
export function historyEntryQuery(client: ApiClient, entryId: string) {
  return queryOptions({
    queryKey: ["history", entryId],
    queryFn: () => unwrap(client.GET("/api/v1/history/{entryId}", { params: { path: { entryId } } })),
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export const collectionsKey = (projectId: string) => ["projects", projectId, "collections"] as const;

/** A project's collections of saved requests, by name. */
export function collectionsQuery(client: ApiClient, projectId: string) {
  return queryOptions({
    queryKey: collectionsKey(projectId),
    queryFn: () => unwrap(client.GET("/api/v1/projects/{projectId}/collections", { params: { path: { projectId } } })),
  });
}

export const itemsKey = (collectionId: string) => ["collections", collectionId, "items"] as const;

/** A collection's folders and requests, each folder's in order. */
export function itemsQuery(client: ApiClient, collectionId: string) {
  return queryOptions({
    queryKey: itemsKey(collectionId),
    queryFn: () => unwrap(client.GET("/api/v1/collections/{collectionId}/items", { params: { path: { collectionId } } })),
  });
}

export const environmentsKey = (projectId: string) => ["projects", projectId, "environments"] as const;

/** A project's environments, in switcher order. */
export function environmentsQuery(client: ApiClient, projectId: string) {
  return queryOptions({
    queryKey: environmentsKey(projectId),
    queryFn: () => unwrap(client.GET("/api/v1/projects/{projectId}/environments", { params: { path: { projectId } } })),
  });
}

export const variablesKey = (environmentId: string) => ["environments", environmentId, "variables"] as const;

/** An environment's variables with their ETag. Secrets come without values. */
export function variablesQuery(client: ApiClient, environmentId: string) {
  return queryOptions({
    queryKey: variablesKey(environmentId),
    queryFn: () => unwrapTagged(client.GET("/api/v1/environments/{environmentId}/variables", { params: { path: { environmentId } } })),
  });
}

export const runsKey = (collectionId: string) => ["collections", collectionId, "runs"] as const;

/** A collection's test runs, newest first. */
export function runsQuery(client: ApiClient, collectionId: string) {
  return infiniteQueryOptions({
    queryKey: runsKey(collectionId),
    queryFn: ({ pageParam }) =>
      unwrap(
        client.GET("/api/v1/collections/{collectionId}/runs", {
          params: { path: { collectionId }, query: { limit: PAGE_SIZE, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });
}

export const runKey = (runId: string) => ["runs", runId] as const;

/** A test run and its totals. Until it finishes, it's asked for again every second. */
export function runQuery(client: ApiClient, runId: string) {
  return queryOptions({
    queryKey: runKey(runId),
    queryFn: () => unwrap(client.GET("/api/v1/runs/{runId}", { params: { path: { runId } } })),
    refetchInterval: (q) => (q.state.data && !running(q.state.data.status) ? false : 1000),
  });
}

/** Whether a run's status means it may still change. */
export function running(status: string): boolean {
  return status === "queued" || status === "running";
}

/** A collection, for pages about it. */
export function collectionQuery(client: ApiClient, collectionId: string) {
  return queryOptions({
    queryKey: ["collections", collectionId],
    queryFn: () => unwrap(client.GET("/api/v1/collections/{collectionId}", { params: { path: { collectionId } } })),
  });
}

export const agentsKey = ["agents"] as const;

/** Your agents. Asked again often, so whether each is online stays current. */
export function agentsQuery(client: ApiClient) {
  return queryOptions({
    queryKey: agentsKey,
    queryFn: () => unwrap(client.GET("/api/v1/agents")),
    refetchInterval: 15_000,
  });
}

export const ingestKeysKey = (environmentId: string) => ["environments", environmentId, "ingest-keys"] as const;

/** The keys that send spans to an environment, without their secrets. */
export function ingestKeysQuery(client: ApiClient, environmentId: string) {
  return queryOptions({
    queryKey: ingestKeysKey(environmentId),
    queryFn: () => unwrap(client.GET("/api/v1/environments/{environmentId}/ingest-keys", { params: { path: { environmentId } } })),
  });
}
