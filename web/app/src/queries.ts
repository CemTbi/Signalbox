// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from "@signalbox/api-client";
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { unwrap } from "./api";

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
