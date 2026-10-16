// SPDX-License-Identifier: Apache-2.0

import type { WorkspaceRole } from "@signalbox/api-client";

export const roleLabels: Record<WorkspaceRole, string> = {
  owner: "Owner",
  admin: "Admin",
  editor: "Editor",
  viewer: "Viewer",
};

export const roleDescriptions: Record<WorkspaceRole, string> = {
  owner: "Can do everything, including deleting the workspace",
  admin: "Can change the workspace, its projects and its members",
  editor: "Can create and change projects",
  viewer: "Can see everything in the workspace",
};

/** Whether a role may invite people and manage members. */
export function canManageMembers(role: WorkspaceRole): boolean {
  return role === "owner" || role === "admin";
}

/** The roles someone with the given role may hand out. Only owners make owners. */
export function assignableRoles(actor: WorkspaceRole): WorkspaceRole[] {
  if (actor === "owner") return ["owner", "admin", "editor", "viewer"];
  if (actor === "admin") return ["admin", "editor", "viewer"];
  return [];
}

/** Whether the actor may change the member's role or remove them. */
export function canManage(actor: WorkspaceRole, member: WorkspaceRole): boolean {
  return actor === "owner" || (actor === "admin" && member !== "owner");
}
