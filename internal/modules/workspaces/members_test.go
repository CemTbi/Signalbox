// SPDX-License-Identifier: Apache-2.0

package workspaces_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func TestListMembers(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, viewer, outsider := f.user(t, "owner"), f.user(t, "viewer"), f.user(t, "outsider")
	w := f.workspace(t, owner, "team")
	f.addMember(t, w.ID, viewer, workspaces.Viewer)

	page, err := f.svc.ListMembers(ctx, viewer, w.ID, workspaces.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	roles := map[uuid.UUID]workspaces.Role{}
	for _, m := range page.Items {
		roles[m.UserID] = m.Role
	}
	if len(roles) != 2 || roles[owner] != workspaces.Owner || roles[viewer] != workspaces.Viewer {
		t.Errorf("members = %+v", page.Items)
	}
	if _, err := f.svc.ListMembers(ctx, outsider, w.ID, workspaces.PageRequest{}); !errors.Is(err, workspaces.ErrNotFound) {
		t.Errorf("outsider: error = %v, want ErrNotFound", err)
	}
}

func TestChangeRole(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, admin, editor := f.user(t, "owner"), f.user(t, "admin"), f.user(t, "editor")
	w := f.workspace(t, owner, "team")
	f.addMember(t, w.ID, admin, workspaces.Admin)
	f.addMember(t, w.ID, editor, workspaces.Editor)

	m, err := f.svc.ChangeRole(ctx, admin, w.ID, editor, workspaces.Viewer)
	if err != nil || m.Role != workspaces.Viewer {
		t.Fatalf("admin demotes editor: %+v, %v", m, err)
	}
	for _, tt := range []struct {
		name          string
		actor, member uuid.UUID
		role          workspaces.Role
		want          error
	}{
		{"admin can't make an owner", admin, editor, workspaces.Owner, workspaces.ErrForbidden},
		{"admin can't demote the owner", admin, owner, workspaces.Admin, workspaces.ErrForbidden},
		{"viewer can't change roles", editor, admin, workspaces.Viewer, workspaces.ErrForbidden},
		{"the last owner stays an owner", owner, owner, workspaces.Admin, workspaces.ErrLastOwner},
		{"no such member", owner, uuid.New(), workspaces.Viewer, workspaces.ErrMemberNotFound},
	} {
		if _, err := f.svc.ChangeRole(ctx, tt.actor, w.ID, tt.member, tt.role); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}

	var fields validate.Errors
	if _, err := f.svc.ChangeRole(ctx, owner, w.ID, editor, "superuser"); !errors.As(err, &fields) {
		t.Errorf("unknown role: error = %v, want a field error", err)
	}

	// With a second owner, the first may step down.
	if _, err := f.svc.ChangeRole(ctx, owner, w.ID, admin, workspaces.Owner); err != nil {
		t.Fatal(err)
	}
	if m, err := f.svc.ChangeRole(ctx, owner, w.ID, owner, workspaces.Admin); err != nil || m.Role != workspaces.Admin {
		t.Errorf("owner steps down with another owner left: %+v, %v", m, err)
	}
}

func TestTwoOwnersCantDemoteEachOtherAtOnce(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a, b := f.user(t, "a"), f.user(t, "b")
	w := f.workspace(t, a, "team")
	f.addMember(t, w.ID, b, workspaces.Owner)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.ChangeRole(ctx, pair[0], w.ID, pair[1], workspaces.Editor)
		}()
	}
	wg.Wait()

	var owners int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner'`, w.ID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Errorf("%d owners left after concurrent demotions (errors %v), want 1", owners, errs)
	}
}

func TestRemoveMember(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, admin, editor, viewer := f.user(t, "owner"), f.user(t, "admin"), f.user(t, "editor"), f.user(t, "viewer")
	w := f.workspace(t, owner, "team")
	f.addMember(t, w.ID, admin, workspaces.Admin)
	f.addMember(t, w.ID, editor, workspaces.Editor)
	f.addMember(t, w.ID, viewer, workspaces.Viewer)

	for _, tt := range []struct {
		name          string
		actor, member uuid.UUID
		want          error
	}{
		{"editor can't remove others", editor, viewer, workspaces.ErrForbidden},
		{"admin can't remove the owner", admin, owner, workspaces.ErrForbidden},
		{"the last owner can't leave", owner, owner, workspaces.ErrLastOwner},
	} {
		if err := f.svc.RemoveMember(ctx, tt.actor, w.ID, tt.member); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	if err := f.svc.RemoveMember(ctx, admin, w.ID, viewer); err != nil {
		t.Errorf("admin removes viewer: %v", err)
	}
	if err := f.svc.RemoveMember(ctx, editor, w.ID, editor); err != nil {
		t.Errorf("editor leaves: %v", err)
	}
	if _, err := f.svc.GetWorkspace(ctx, editor, w.ID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Errorf("after leaving: error = %v, want ErrNotFound", err)
	}
	if err := f.svc.RemoveMember(ctx, owner, w.ID, viewer); !errors.Is(err, workspaces.ErrMemberNotFound) {
		t.Errorf("removing someone twice: error = %v, want ErrMemberNotFound", err)
	}
}

func TestInvitations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, admin, editor := f.user(t, "owner"), f.user(t, "admin"), f.user(t, "editor")
	w := f.workspace(t, owner, "team")
	f.addMember(t, w.ID, admin, workspaces.Admin)
	f.addMember(t, w.ID, editor, workspaces.Editor)

	inv, err := f.svc.Invite(ctx, admin, w.ID, workspaces.InvitationInput{Email: " Grace@Example.com ", Role: workspaces.Editor})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Email != "Grace@Example.com" || len(inv.Token) != 43 || inv.InvitedBy != admin ||
		inv.ExpiresAt.Sub(inv.CreatedAt) != workspaces.InvitationLifetime {
		t.Errorf("Invite = %+v", inv)
	}

	for _, tt := range []struct {
		name  string
		actor uuid.UUID
		role  workspaces.Role
		want  error
	}{
		{"editors can't invite", editor, workspaces.Viewer, workspaces.ErrForbidden},
		{"admins can't invite owners", admin, workspaces.Owner, workspaces.ErrForbidden},
		{"outsiders can't invite", uuid.New(), workspaces.Viewer, workspaces.ErrNotFound},
	} {
		_, err := f.svc.Invite(ctx, tt.actor, w.ID, workspaces.InvitationInput{Email: "x@example.com", Role: tt.role})
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}

	details, err := f.svc.LookupInvitation(ctx, inv.Token)
	if err != nil || details.WorkspaceName != w.Name || details.Role != workspaces.Editor {
		t.Errorf("LookupInvitation = %+v, %v", details, err)
	}

	// Inviting the same address again replaces the first link.
	again, err := f.svc.Invite(ctx, owner, w.ID, workspaces.InvitationInput{Email: "grace@example.com", Role: workspaces.Viewer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LookupInvitation(ctx, inv.Token); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("the replaced link still works: %v", err)
	}
	open, err := f.svc.ListInvitations(ctx, admin, w.ID, workspaces.PageRequest{})
	if err != nil || len(open.Items) != 1 || open.Items[0].ID != again.ID {
		t.Errorf("ListInvitations = %+v, %v", open, err)
	}
	if _, err := f.svc.ListInvitations(ctx, editor, w.ID, workspaces.PageRequest{}); !errors.Is(err, workspaces.ErrForbidden) {
		t.Errorf("editor lists invitations: error = %v, want ErrForbidden", err)
	}

	grace := f.user(t, "grace")
	if _, err := f.svc.AcceptInvitation(ctx, again.Token, grace, "someone.else@example.com"); !errors.Is(err, workspaces.ErrEmailMismatch) {
		t.Errorf("accepting another address's invitation: error = %v, want ErrEmailMismatch", err)
	}
	joined, err := f.svc.AcceptInvitation(ctx, again.Token, grace, "GRACE@example.com")
	if err != nil || joined.ID != w.ID || joined.Role != workspaces.Viewer {
		t.Fatalf("AcceptInvitation = %+v, %v", joined, err)
	}
	if _, err := f.svc.AcceptInvitation(ctx, again.Token, grace, "grace@example.com"); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("a used link works again: %v", err)
	}
}

func TestAcceptingKeepsAnExistingRole(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, admin := f.user(t, "owner"), f.user(t, "admin")
	w := f.workspace(t, owner, "team")
	f.addMember(t, w.ID, admin, workspaces.Admin)

	inv, err := f.svc.Invite(ctx, owner, w.ID, workspaces.InvitationInput{Email: "admin@example.com", Role: workspaces.Viewer})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := f.svc.AcceptInvitation(ctx, inv.Token, admin, "admin@example.com")
	if err != nil || joined.Role != workspaces.Admin {
		t.Errorf("AcceptInvitation by a member = %+v, %v; want them to stay admin", joined, err)
	}
}

func TestExpiredAndRevokedInvitations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	owner, outsider := f.user(t, "owner"), f.user(t, "outsider")
	w := f.workspace(t, owner, "team")

	expired, err := f.svc.Invite(ctx, owner, w.ID, workspaces.InvitationInput{Email: "late@example.com", Role: workspaces.Viewer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE workspace_invitations SET expires_at = now() - interval '1 second' WHERE id = $1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LookupInvitation(ctx, expired.Token); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("expired link: error = %v, want ErrInvitationNotFound", err)
	}
	if n, err := f.svc.RemoveExpiredInvitations(ctx); err != nil || n != 1 {
		t.Errorf("RemoveExpiredInvitations = %d, %v, want 1", n, err)
	}

	open, err := f.svc.Invite(ctx, owner, w.ID, workspaces.InvitationInput{Email: "soon@example.com", Role: workspaces.Viewer})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeInvitation(ctx, outsider, open.ID); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("outsider revokes: error = %v, want ErrInvitationNotFound", err)
	}
	if err := f.svc.RevokeInvitation(ctx, owner, open.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LookupInvitation(ctx, open.Token); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("revoked link: error = %v, want ErrInvitationNotFound", err)
	}
	if _, err := f.svc.LookupInvitation(ctx, "not-a-token"); !errors.Is(err, workspaces.ErrInvitationNotFound) {
		t.Errorf("malformed token: error = %v, want ErrInvitationNotFound", err)
	}
}
