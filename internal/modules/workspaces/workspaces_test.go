// SPDX-License-Identifier: Apache-2.0

package workspaces_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

type fixture struct {
	svc  *workspaces.Service
	pool *pgxpool.Pool
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := postgrestest.New(t)
	return fixture{svc: workspaces.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool: pool}
}

// user creates an account directly; this module only needs the ID.
func (f fixture) user(t *testing.T, name string) uuid.UUID {
	t.Helper()
	u := id.New()
	_, err := f.pool.Exec(context.Background(), `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`,
		u, name+"@example.com", name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) addMember(t *testing.T, workspaceID, userID uuid.UUID, role workspaces.Role) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, workspaceID, userID, role)
	if err != nil {
		t.Fatal(err)
	}
}

func (f fixture) workspace(t *testing.T, owner uuid.UUID, slug string) workspaces.Workspace {
	t.Helper()
	w, err := f.svc.CreateWorkspace(context.Background(), owner, workspaces.WorkspaceInput{Name: "Team " + slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestCreateWorkspaceMakesTheCreatorOwner(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")

	w, err := f.svc.CreateWorkspace(ctx, ada, workspaces.WorkspaceInput{Name: "  Payments  ", Slug: "payments"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "Payments" || w.Slug != "payments" || w.Role != workspaces.Owner || w.Version != 1 || w.CreatedAt.IsZero() {
		t.Errorf("CreateWorkspace = %+v", w)
	}
	got, err := f.svc.GetWorkspace(ctx, ada, w.ID)
	if err != nil || got != w {
		t.Errorf("GetWorkspace = %+v, %v, want %+v", got, err, w)
	}

	_, err = f.svc.CreateWorkspace(ctx, ada, workspaces.WorkspaceInput{Name: "Other", Slug: "payments"})
	if !errors.Is(err, workspaces.ErrSlugTaken) {
		t.Errorf("duplicate slug: error = %v, want ErrSlugTaken", err)
	}

	_, err = f.svc.CreateWorkspace(ctx, ada, workspaces.WorkspaceInput{Name: "", Slug: "Not Valid"})
	var fields validate.Errors
	if !errors.As(err, &fields) || len(fields) != 2 {
		t.Errorf("invalid input: error = %v, want two field errors", err)
	}
}

func TestNonMembersSeeNothing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada, eve := f.user(t, "ada"), f.user(t, "eve")
	w := f.workspace(t, ada, "payments")
	p, err := f.svc.CreateProject(ctx, ada, w.ID, workspaces.ProjectInput{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}

	checks := map[string]error{}
	_, checks["GetWorkspace"] = f.svc.GetWorkspace(ctx, eve, w.ID)
	_, checks["UpdateWorkspace"] = f.svc.UpdateWorkspace(ctx, eve, w.ID, workspaces.Precondition{}, workspaces.WorkspacePatch{Name: ptr("Mine")})
	checks["DeleteWorkspace"] = f.svc.DeleteWorkspace(ctx, eve, w.ID, workspaces.Precondition{})
	_, checks["ListProjects"] = f.svc.ListProjects(ctx, eve, w.ID, workspaces.PageRequest{})
	_, checks["CreateProject"] = f.svc.CreateProject(ctx, eve, w.ID, workspaces.ProjectInput{Name: "Mine"})
	_, checks["GetProject"] = f.svc.GetProject(ctx, eve, p.ID)
	_, checks["UpdateProject"] = f.svc.UpdateProject(ctx, eve, p.ID, workspaces.Precondition{}, workspaces.ProjectPatch{Name: ptr("Mine")})
	checks["DeleteProject"] = f.svc.DeleteProject(ctx, eve, p.ID, workspaces.Precondition{})
	for op, err := range checks {
		if !errors.Is(err, workspaces.ErrNotFound) {
			t.Errorf("%s by a non-member: error = %v, want ErrNotFound", op, err)
		}
	}

	page, err := f.svc.ListWorkspaces(ctx, eve, workspaces.PageRequest{})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("ListWorkspaces for a non-member = %+v, %v", page, err)
	}
}

func TestRolesDecideWhatMembersMayChange(t *testing.T) {
	type outcome map[string]bool // operation -> allowed
	tests := []struct {
		role workspaces.Role
		want outcome
	}{
		{workspaces.Owner, outcome{"updateWorkspace": true, "deleteWorkspace": true, "createProject": true, "updateProject": true, "deleteProject": true}},
		{workspaces.Admin, outcome{"updateWorkspace": true, "deleteWorkspace": false, "createProject": true, "updateProject": true, "deleteProject": true}},
		{workspaces.Editor, outcome{"updateWorkspace": false, "deleteWorkspace": false, "createProject": true, "updateProject": true, "deleteProject": false}},
		{workspaces.Viewer, outcome{"updateWorkspace": false, "deleteWorkspace": false, "createProject": false, "updateProject": false, "deleteProject": false}},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			owner, member := f.user(t, "owner"), f.user(t, "member")
			w := f.workspace(t, owner, "team")
			if tt.role != workspaces.Owner {
				f.addMember(t, w.ID, member, tt.role)
			} else {
				member = owner
			}
			p, err := f.svc.CreateProject(ctx, owner, w.ID, workspaces.ProjectInput{Name: "Shop"})
			if err != nil {
				t.Fatal(err)
			}

			// Every member can read.
			if got, err := f.svc.GetWorkspace(ctx, member, w.ID); err != nil || got.Role != tt.role {
				t.Errorf("GetWorkspace = %+v, %v", got, err)
			}
			if _, err := f.svc.GetProject(ctx, member, p.ID); err != nil {
				t.Errorf("GetProject: %v", err)
			}

			got := outcome{}
			_, err = f.svc.UpdateWorkspace(ctx, member, w.ID, workspaces.Precondition{}, workspaces.WorkspacePatch{Name: ptr("Renamed")})
			got["updateWorkspace"] = allowed(t, err)
			_, err = f.svc.CreateProject(ctx, member, w.ID, workspaces.ProjectInput{Name: "Another"})
			got["createProject"] = allowed(t, err)
			_, err = f.svc.UpdateProject(ctx, member, p.ID, workspaces.Precondition{}, workspaces.ProjectPatch{Description: ptr("New")})
			got["updateProject"] = allowed(t, err)
			got["deleteProject"] = allowed(t, f.svc.DeleteProject(ctx, member, p.ID, workspaces.Precondition{}))
			got["deleteWorkspace"] = allowed(t, f.svc.DeleteWorkspace(ctx, member, w.ID, workspaces.Precondition{}))

			for op, want := range tt.want {
				if got[op] != want {
					t.Errorf("%s: allowed = %v, want %v", op, got[op], want)
				}
			}
		})
	}
}

func allowed(t *testing.T, err error) bool {
	t.Helper()
	switch {
	case err == nil:
		return true
	case errors.Is(err, workspaces.ErrForbidden):
		return false
	}
	t.Fatalf("unexpected error: %v", err)
	return false
}

func TestPreconditionsDetectConcurrentEdits(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")
	w := f.workspace(t, ada, "payments")

	updated, err := f.svc.UpdateWorkspace(ctx, ada, w.ID, workspaces.Precondition{Versions: []int64{w.Version}},
		workspaces.WorkspacePatch{Name: ptr("Payments platform")})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != w.Version+1 || updated.Name != "Payments platform" || updated.Slug != "payments" {
		t.Errorf("UpdateWorkspace = %+v", updated)
	}

	// A client still holding the old version loses.
	_, err = f.svc.UpdateWorkspace(ctx, ada, w.ID, workspaces.Precondition{Versions: []int64{w.Version}},
		workspaces.WorkspacePatch{Slug: ptr("pay")})
	if !errors.Is(err, workspaces.ErrVersionMismatch) {
		t.Errorf("stale update: error = %v, want ErrVersionMismatch", err)
	}
	err = f.svc.DeleteWorkspace(ctx, ada, w.ID, workspaces.Precondition{Versions: []int64{w.Version}})
	if !errors.Is(err, workspaces.ErrVersionMismatch) {
		t.Errorf("stale delete: error = %v, want ErrVersionMismatch", err)
	}
	if err := f.svc.DeleteWorkspace(ctx, ada, w.ID, workspaces.Precondition{Versions: []int64{updated.Version}}); err != nil {
		t.Errorf("current delete: %v", err)
	}
	if _, err := f.svc.GetWorkspace(ctx, ada, w.ID); !errors.Is(err, workspaces.ErrNotFound) {
		t.Errorf("after delete: error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentUpdatesWithTheSameVersionLetOneWin(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")
	w := f.workspace(t, ada, "payments")

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.UpdateWorkspace(ctx, ada, w.ID, workspaces.Precondition{Versions: []int64{w.Version}},
				workspaces.WorkspacePatch{Name: ptr(fmt.Sprintf("Name %d", i))})
			switch {
			case err == nil:
				mu.Lock()
				wins++
				mu.Unlock()
			case !errors.Is(err, workspaces.ErrVersionMismatch):
				t.Errorf("UpdateWorkspace: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Errorf("%d concurrent updates won, want exactly 1", wins)
	}
}

func TestUpdateWorkspaceRejectsTakenSlug(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")
	f.workspace(t, ada, "payments")
	w := f.workspace(t, ada, "billing")
	_, err := f.svc.UpdateWorkspace(ctx, ada, w.ID, workspaces.Precondition{}, workspaces.WorkspacePatch{Slug: ptr("payments")})
	if !errors.Is(err, workspaces.ErrSlugTaken) {
		t.Errorf("error = %v, want ErrSlugTaken", err)
	}
}

func TestPagination(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")
	var want []uuid.UUID
	for i := range 5 {
		want = append(want, f.workspace(t, ada, fmt.Sprintf("team-%d", i)).ID)
	}

	var got []uuid.UUID
	req := workspaces.PageRequest{Limit: 2}
	for pages := 0; ; pages++ {
		page, err := f.svc.ListWorkspaces(ctx, ada, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range page.Items {
			got = append(got, w.ID)
		}
		if !page.More {
			if pages != 2 {
				t.Errorf("listed %d pages, want 3", pages+1)
			}
			break
		}
		req.After = page.Items[len(page.Items)-1].ID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("listed %v, want %v in creation order", got, want)
	}

	empty, err := f.svc.ListProjects(ctx, ada, want[0], workspaces.PageRequest{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.More {
		t.Errorf("empty project list = %+v, %v; want an empty, non-nil page", empty, err)
	}
}

func TestCreateProjectAddsDefaultEnvironments(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ada := f.user(t, "ada")
	w := f.workspace(t, ada, "payments")

	p, err := f.svc.CreateProject(ctx, ada, w.ID, workspaces.ProjectInput{Name: "Shop", Description: " Checkout and orders. "})
	if err != nil {
		t.Fatal(err)
	}
	if p.WorkspaceID != w.ID || p.Description != "Checkout and orders." || p.Version != 1 {
		t.Errorf("CreateProject = %+v", p)
	}
	rows, err := f.pool.Query(ctx, `SELECT slug || ':' || kind || ':' || position FROM environments WHERE project_id = $1 ORDER BY position`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var envs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		envs = append(envs, s)
	}
	if want := "[development:development:0 staging:staging:1 production:production:2]"; fmt.Sprint(envs) != want {
		t.Errorf("environments = %v, want %s", envs, want)
	}

	// Removing the description with an empty value.
	updated, err := f.svc.UpdateProject(ctx, ada, p.ID, workspaces.Precondition{Versions: []int64{p.Version}},
		workspaces.ProjectPatch{Description: ptr("")})
	if err != nil || updated.Description != "" || updated.Name != "Shop" {
		t.Errorf("UpdateProject = %+v, %v", updated, err)
	}

	// Deleting the workspace removes its projects and environments.
	if err := f.svc.DeleteWorkspace(ctx, ada, w.ID, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM projects) + (SELECT count(*) FROM environments)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d projects and environments outlived their workspace", left)
	}
}

func ptr[T any](v T) *T { return &v }
