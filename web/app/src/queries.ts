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
