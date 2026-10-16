// SPDX-License-Identifier: Apache-2.0

import type { Member, NewInvitation, WorkspaceRole } from "@signalbox/api-client";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, useNavigate, useRouteContext } from "@tanstack/react-router";
import { useState } from "react";
import { api, describeError, unwrap } from "../../api";
import { LoadError, Loading } from "../../components/Page";
import { invitationsKey, invitationsQuery, membersKey, membersQuery, workspaceQuery, workspacesKey } from "../../queries";
import { RoleBadge } from "../workspaces/pages";
import { assignableRoles, canManage, canManageMembers, roleLabels } from "../workspaces/roles";
import { InvitationLink } from "./InvitationLink";
import { InviteForm, type InviteValues } from "./InviteForm";

const membersRoute = getRouteApi("/_app/workspaces/$workspaceId/members");
const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

export function WorkspaceMembers() {
  const { workspaceId } = membersRoute.useParams();
  const { session } = useRouteContext({ from: "/_app" });
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const workspace = useQuery(workspaceQuery(api, workspaceId));
  const actorRole: WorkspaceRole = workspace.data?.role ?? "viewer";
  const manager = canManageMembers(actorRole);
  const members = useInfiniteQuery(membersQuery(api, workspaceId));
  const invitations = useInfiniteQuery({ ...invitationsQuery(api, workspaceId), enabled: manager });
  const [created, setCreated] = useState<NewInvitation | null>(null);

  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: membersKey(workspaceId) }),
      queryClient.invalidateQueries({ queryKey: invitationsKey(workspaceId) }),
    ]);

  const changeRole = useMutation({
    mutationFn: ({ member, role }: { member: Member; role: WorkspaceRole }) =>
      unwrap(
        api.PATCH("/api/v1/workspaces/{workspaceId}/members/{userId}", {
          params: { path: { workspaceId, userId: member.user.id } },
          body: { role },
        }),
      ),
    onSettled: refresh,
  });
  const remove = useMutation({
    mutationFn: (member: Member) =>
      unwrap(
        api.DELETE("/api/v1/workspaces/{workspaceId}/members/{userId}", {
          params: { path: { workspaceId, userId: member.user.id } },
        }),
      ),
    onSuccess: async (_, member) => {
      if (member.user.id === session.user.id) {
        // Leaving takes the workspace away; go back to the list.
        await queryClient.invalidateQueries({ queryKey: workspacesKey });
        await navigate({ to: "/workspaces" });
        return;
      }
      await refresh();
    },
  });
  const invite = useMutation({
    mutationFn: (values: InviteValues) =>
      unwrap(api.POST("/api/v1/workspaces/{workspaceId}/invitations", { params: { path: { workspaceId } }, body: values })),
    onSuccess: async (invitation) => {
      setCreated(invitation);
      await queryClient.invalidateQueries({ queryKey: invitationsKey(workspaceId) });
    },
  });
  const revoke = useMutation({
    mutationFn: (invitationId: string) =>
      unwrap(api.DELETE("/api/v1/invitations/{invitationId}", { params: { path: { invitationId } } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: invitationsKey(workspaceId) }),
  });

  const list = members.data?.pages.flatMap((p) => p.data) ?? [];
  const owners = list.filter((m) => m.role === "owner").length;
  const open = invitations.data?.pages.flatMap((p) => p.data) ?? [];
  const memberError = changeRole.error ?? remove.error;

  return (
    <div className="split">
      <section aria-labelledby="members" className="split-main stack-tight">
        <h2 id="members" className="section-title">
          Members
        </h2>
        {memberError ? (
          <p className="form-alert" role="alert">
            {describeError(memberError)}
          </p>
        ) : null}
        {members.isPending ? <Loading label="Loading members…" /> : null}
        {members.isError ? (
          <LoadError message={describeError(members.error)} onRetry={() => void members.refetch()} />
        ) : null}
        {list.length > 0 ? (
          <ul className="rows">
            {list.map((m) => (
              <MemberRow
                key={m.user.id}
                member={m}
                actorRole={actorRole}
                isSelf={m.user.id === session.user.id}
                lastOwner={m.role === "owner" && owners === 1 && !members.hasNextPage}
                busy={changeRole.isPending || remove.isPending}
                onRoleChange={(role) => changeRole.mutate({ member: m, role })}
                onRemove={() => remove.mutate(m)}
              />
            ))}
          </ul>
        ) : null}

        {manager && open.length > 0 ? (
          <>
            <h2 id="open-invitations" className="section-title">
              Open invitations
            </h2>
            {revoke.error ? (
              <p className="form-alert" role="alert">
                {describeError(revoke.error)}
              </p>
            ) : null}
            <ul className="rows" aria-labelledby="open-invitations">
              {open.map((inv) => (
                <li key={inv.id} className="row">
                  <span className="row-main">
                    <span className="row-title">{inv.email}</span>
                    <span className="row-sub">Expires on {dateFormat.format(new Date(inv.expiresAt))}</span>
                  </span>
                  <span className="row-actions">
                    <RoleBadge role={inv.role} />
                    <button
                      type="button"
                      className="button ghost"
                      disabled={revoke.isPending}
                      onClick={() => revoke.mutate(inv.id)}
                      aria-label={`Revoke the invitation for ${inv.email}`}
                    >
                      Revoke
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          </>
        ) : null}
      </section>

      <section aria-labelledby="invite" className="panel split-side">
        <h2 id="invite" className="section-title">
          Invite people
        </h2>
        {!manager ? (
          <p className="muted">Only workspace owners and admins can invite people.</p>
        ) : created ? (
          <InvitationLink invitation={created} onDone={() => setCreated(null)} />
        ) : (
          <InviteForm
            actorRole={actorRole}
            onSubmit={(v) => invite.mutate(v)}
            pending={invite.isPending}
            error={invite.error}
          />
        )}
      </section>
    </div>
  );
}

function MemberRow({
  member,
  actorRole,
  isSelf,
  lastOwner,
  busy,
  onRoleChange,
  onRemove,
}: {
  member: Member;
  actorRole: WorkspaceRole;
  isSelf: boolean;
  // The last owner can't step down or leave; the server would refuse.
  lastOwner: boolean;
  busy: boolean;
  onRoleChange: (role: WorkspaceRole) => void;
  onRemove: () => void;
}) {
  const editable = canManage(actorRole, member.role) && !lastOwner;
  const name = isSelf ? `${member.user.name} (you)` : member.user.name;
  return (
    <li className="row">
      <span className="row-main">
        <span className="row-title">{name}</span>
        <span className="row-sub">{member.user.email}</span>
      </span>
      <span className="row-actions">
        {editable ? (
          <select
            aria-label={`Role of ${member.user.name}`}
            value={member.role}
            disabled={busy}
            onChange={(e) => onRoleChange(e.target.value as WorkspaceRole)}
          >
            {assignableRoles(actorRole)
              .concat(assignableRoles(actorRole).includes(member.role) ? [] : [member.role])
              .map((r) => (
                <option key={r} value={r}>
                  {roleLabels[r]}
                </option>
              ))}
          </select>
        ) : (
          <RoleBadge role={member.role} />
        )}
        {(isSelf && !lastOwner) || editable ? (
          <button
            type="button"
            className="button ghost"
            disabled={busy}
            onClick={onRemove}
            aria-label={isSelf ? "Leave this workspace" : `Remove ${member.user.name}`}
          >
            {isSelf ? "Leave" : "Remove"}
          </button>
        ) : null}
      </span>
    </li>
  );
}
