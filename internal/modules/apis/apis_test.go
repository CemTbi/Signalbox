// SPDX-License-Identifier: Apache-2.0

package apis_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

const ordersV1 = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.0.0
  description: Places and tracks orders.
  contact: {email: team@example.com}
  license: {name: Apache-2.0, identifier: Apache-2.0}
servers:
  - url: https://orders.example.com
tags:
  - name: orders
    description: Orders.
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      summary: Get an order
      description: Returns one order.
      tags: [orders]
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema: {type: object}
`

type fixture struct {
	ws      *workspaces.Service
	mod     *apis.Module
	pool    *pgxpool.Pool
	owner   uuid.UUID
	project uuid.UUID
	ctx     context.Context
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := postgrestest.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ws := workspaces.New(pool, logger)
	f := fixture{ws: ws, mod: apis.New(pool, logger, ws), pool: pool, ctx: context.Background()}
	f.owner = f.user(t, "ada")
	w, err := ws.CreateWorkspace(f.ctx, f.owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(f.ctx, f.owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	f.project = p.ID
	return f
}

func (f fixture) user(t *testing.T, name string) uuid.UUID {
	t.Helper()
	u := id.New()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, u, name+"@example.com", name); err != nil {
		t.Fatal(err)
	}
	return u
}

// member adds a user with a role to the project's workspace.
func (f fixture) member(t *testing.T, name string, role workspaces.Role) uuid.UUID {
	t.Helper()
	u := f.user(t, name)
	p, err := f.ws.GetProject(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`,
		p.WorkspaceID, u, role); err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) service(t *testing.T, slug string) apis.Service {
	t.Helper()
	s, err := f.mod.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pre(v int64) workspaces.Precondition { return workspaces.Precondition{Versions: []int64{v}} }

func TestServices(t *testing.T) {
	f := setup(t)
	s, err := f.mod.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{
		Name: " Orders API ", Slug: "orders-api", TelemetryServiceName: "orders", Position: &apis.Position{X: 320, Y: 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Orders API" || s.Version != 1 || s.Position.X != 320 || s.TelemetryServiceName != "orders" {
		t.Errorf("created %+v", s)
	}
	if _, err := f.mod.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: "Again", Slug: "orders-api"}); !errors.Is(err, apis.ErrSlugTaken) {
		t.Errorf("duplicate slug: %v", err)
	}
	var verrs validate.Errors
	if _, err := f.mod.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: "", Slug: "Bad Slug"}); !errors.As(err, &verrs) || len(verrs) != 2 {
		t.Errorf("invalid input: %v", err)
	}

	// Moving the node, with optimistic locking.
	to := apis.Position{X: 480, Y: 200}
	moved, err := f.mod.UpdateService(f.ctx, f.owner, s.ID, pre(1), apis.ServicePatch{Position: &to})
	if err != nil || moved.Position != to || moved.Version != 2 || moved.Name != "Orders API" {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	if _, err := f.mod.UpdateService(f.ctx, f.owner, s.ID, pre(1), apis.ServicePatch{Position: &to}); !errors.Is(err, apis.ErrVersionMismatch) {
		t.Errorf("stale precondition: %v", err)
	}
	clear := ""
	if u, err := f.mod.UpdateService(f.ctx, f.owner, s.ID, workspaces.Precondition{}, apis.ServicePatch{TelemetryServiceName: &clear}); err != nil || u.TelemetryServiceName != "" {
		t.Errorf("clearing the telemetry name: %+v, %v", u, err)
	}

	page, err := f.mod.ListServices(f.ctx, f.owner, f.project, workspaces.PageRequest{})
	if err != nil || len(page.Items) != 1 || page.More {
		t.Errorf("list = %+v, %v", page, err)
	}
	if err := f.mod.DeleteService(f.ctx, f.owner, s.ID, pre(1)); !errors.Is(err, apis.ErrVersionMismatch) {
		t.Errorf("delete with a stale version: %v", err)
	}
	if err := f.mod.DeleteService(f.ctx, f.owner, s.ID, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.GetService(f.ctx, f.owner, s.ID); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("deleted service: %v", err)
	}
}

func TestRoles(t *testing.T) {
	f := setup(t)
	editor := f.member(t, "grace", workspaces.Editor)
	viewer := f.member(t, "linus", workspaces.Viewer)
	stranger := f.user(t, "mallory")

	s, err := f.mod.CreateService(f.ctx, editor, f.project, apis.ServiceInput{Name: "Orders", Slug: "orders"})
	if err != nil {
		t.Fatalf("an editor can't add a service: %v", err)
	}
	if _, err := f.mod.CreateService(f.ctx, viewer, f.project, apis.ServiceInput{Name: "X", Slug: "x"}); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("viewer adds a service: %v", err)
	}
	if _, err := f.mod.GetService(f.ctx, viewer, s.ID); err != nil {
		t.Errorf("a viewer can't read: %v", err)
	}
	if _, err := f.mod.GetService(f.ctx, stranger, s.ID); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("a stranger reads a service: %v", err)
	}
	if _, err := f.mod.ListServices(f.ctx, stranger, f.project, workspaces.PageRequest{}); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("a stranger lists services: %v", err)
	}

	spec, _, err := f.mod.CreateSpec(f.ctx, editor, s.ID, "Orders API", ordersV1)
	if err != nil {
		t.Fatalf("an editor can't add a spec: %v", err)
	}
	if _, _, err := f.mod.CreateRevision(f.ctx, viewer, spec.ID, pre(spec.Version), ordersV1, ""); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("a viewer saves a revision: %v", err)
	}
	if _, err := f.mod.RevisionReport(f.ctx, stranger, spec.CurrentRevisionID); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("a stranger reads a report: %v", err)
	}
	// Deleting needs an admin, as for projects.
	if err := f.mod.DeleteSpec(f.ctx, editor, spec.ID, workspaces.Precondition{}); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("an editor deletes a spec: %v", err)
	}
	if err := f.mod.DeleteService(f.ctx, editor, s.ID, workspaces.Precondition{}); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("an editor deletes a service: %v", err)
	}
}

func TestSpecRevisions(t *testing.T) {
	f := setup(t)
	s := f.service(t, "orders")

	spec, first, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "Orders API", ordersV1)
	if err != nil {
		t.Fatal(err)
	}
	if spec.CurrentRevisionID != first.ID || first.Number != 1 || first.FileType != "yaml" || first.Format != "openapi-3.1" ||
		first.OpenAPIVersion != "3.1.1" || first.Title != "Orders API" || first.APIVersion != "1.0.0" ||
		first.SizeBytes != len(ordersV1) || first.SHA256 != sha256.Sum256([]byte(ordersV1)) || first.CreatedBy != f.owner {
		t.Errorf("first revision = %+v", first)
	}

	// A save needs the spec's current version.
	v2 := strings.Replace(ordersV1, "version: 1.0.0", "version: 1.1.0", 1)
	if _, _, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, workspaces.Precondition{}, v2, "Bump"); !errors.Is(err, apis.ErrPreconditionRequired) {
		t.Errorf("save without If-Match: %v", err)
	}
	spec2, second, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, pre(spec.Version), v2, "  Bump the version ")
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != 2 || second.Message != "Bump the version" || second.APIVersion != "1.1.0" ||
		spec2.CurrentRevisionID != second.ID || spec2.Version != spec.Version+1 {
		t.Errorf("second revision = %+v, spec %+v", second, spec2)
	}
	// Someone who loaded the spec before that save can't overwrite it.
	if _, _, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, pre(spec.Version), ordersV1, "Stale"); !errors.Is(err, apis.ErrVersionMismatch) {
		t.Errorf("stale save: %v", err)
	}

	// A document with problems is saved, with its report.
	broken := ordersWithProblems
	_, third, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, pre(spec2.Version), broken, "")
	if err != nil {
		t.Fatal(err)
	}
	if third.Counts.Warnings == 0 && third.Counts.Errors == 0 {
		t.Errorf("third revision has no problems: %+v", third.Counts)
	}
	report, err := f.mod.RevisionReport(f.ctx, f.owner, third.ID)
	if err != nil || len(report.Diagnostics) != third.Counts.Errors+third.Counts.Warnings+third.Counts.Infos+third.Counts.Hints {
		t.Errorf("report = %+v, %v", report.Counts, err)
	}
	content, fileType, err := f.mod.RevisionContent(f.ctx, f.owner, first.ID)
	if err != nil || content != ordersV1 || fileType != "yaml" {
		t.Errorf("content of the first revision changed: %v", err)
	}

	// History, newest first, in pages.
	p1, err := f.mod.ListRevisions(f.ctx, f.owner, spec.ID, workspaces.PageRequest{Limit: 2})
	if err != nil || len(p1.Items) != 2 || !p1.More || p1.Items[0].Number != 3 || p1.Items[1].Number != 2 {
		t.Fatalf("first page = %+v, %v", p1, err)
	}
	p2, err := f.mod.ListRevisions(f.ctx, f.owner, spec.ID, workspaces.PageRequest{Limit: 2, After: p1.Items[1].ID})
	if err != nil || len(p2.Items) != 1 || p2.More || p2.Items[0].Number != 1 {
		t.Errorf("second page = %+v, %v", p2, err)
	}
}

func TestDocumentsThatCantBeSaved(t *testing.T) {
	f := setup(t)
	s := f.service(t, "orders")
	var unreadable *apis.UnreadableError
	for name, doc := range map[string]string{
		"not YAML":            "openapi: [3.1.0\n",
		"not OpenAPI":         "hello: world\n",
		"unsupported version": "openapi: 4.0.0\ninfo: {title: x, version: '1'}\npaths: {}\n",
	} {
		if _, _, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "X", doc); !errors.As(err, &unreadable) {
			t.Errorf("%s: error = %v, want UnreadableError", name, err)
		}
	}
	var verrs validate.Errors
	if _, _, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "X", "  \n"); !errors.As(err, &verrs) || verrs[0].Pointer != "/content" {
		t.Errorf("empty document: %v", err)
	}
	if _, _, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "X", strings.Repeat("a", apis.MaxSpecBytes+1)); !errors.As(err, &verrs) {
		t.Errorf("too large: %v", err)
	}
	// JSON documents are recognized as such.
	_, rev, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "JSON", `{"openapi":"3.0.3","info":{"title":"T","version":"2"},"paths":{}}`)
	if err != nil || rev.FileType != "json" || rev.Format != "openapi-3.0" {
		t.Errorf("JSON spec = %+v, %v", rev, err)
	}
}

func TestDeletingAServiceDeletesItsSpecs(t *testing.T) {
	f := setup(t)
	s := f.service(t, "orders")
	spec, rev, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "Orders API", ordersV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.mod.DeleteService(f.ctx, f.owner, s.ID, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.GetSpec(f.ctx, f.owner, spec.ID); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("spec after its service was deleted: %v", err)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM spec_revisions WHERE id = $1`, rev.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d revisions left, %v", n, err)
	}
}

// ordersWithProblems has an operation without an operationId and one
// without responses described.
const ordersWithProblems = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.2.0
paths:
  /orders/{id}:
    get:
      responses:
        '200':
          description: The order.
  /refunds:
    get:
      responses: {}
`

// ordersWithErrors has a reference that doesn't resolve, an error.
const ordersWithErrors = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.3.0
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        '200':
          $ref: '#/components/responses/Missing'
`

func TestPublishingVersions(t *testing.T) {
	f := setup(t)
	s := f.service(t, "orders")
	spec, first, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "Orders API", ordersV1)
	if err != nil {
		t.Fatal(err)
	}

	// The current revision, named after the document's own version.
	v1, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{Notes: "  First release "})
	if err != nil {
		t.Fatal(err)
	}
	if v1.Name != "1.0.0" || v1.RevisionID != first.ID || v1.RevisionNumber != 1 || v1.Notes != "First release" || v1.PublishedBy != f.owner {
		t.Errorf("v1 = %+v", v1)
	}
	if _, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{}); !errors.Is(err, apis.ErrVersionExists) {
		t.Errorf("publishing 1.0.0 again: %v", err)
	}

	// A revision with errors can't be published; one with only warnings can.
	spec2, broken, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, pre(spec.Version), ordersWithErrors, "")
	if err != nil {
		t.Fatal(err)
	}
	var hasErrors *apis.HasErrorsError
	if _, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{}); !errors.As(err, &hasErrors) || hasErrors.Errors != broken.Counts.Errors {
		t.Errorf("publishing a revision with %d errors: %v", broken.Counts.Errors, err)
	}
	_, warned, err := f.mod.CreateRevision(f.ctx, f.owner, spec.ID, pre(spec2.Version), ordersWarningsOnly, "")
	if err != nil {
		t.Fatal(err)
	}
	if warned.Counts.Errors != 0 || warned.Counts.Warnings == 0 {
		t.Fatalf("expected warnings only: %+v", warned.Counts)
	}
	v2, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{Name: "1.2.0-beta.1"})
	if err != nil || v2.RevisionNumber != 3 {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}

	// An older revision by ID.
	old, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{RevisionID: first.ID, Name: "1.0.0+hotfix"})
	if err != nil || old.RevisionNumber != 1 {
		t.Errorf("publishing revision 1 again: %+v, %v", old, err)
	}
	var verrs validate.Errors
	for _, bad := range []string{"-1", "1.0 beta", strings.Repeat("9", 65), "Latest"} {
		if _, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{Name: bad}); !errors.As(err, &verrs) {
			t.Errorf("name %q: %v", bad, err)
		}
	}
	// A revision of another spec isn't this spec's.
	other, otherRev, err := f.mod.CreateSpec(f.ctx, f.owner, s.ID, "Other", ordersV1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.PublishVersion(f.ctx, f.owner, spec.ID, apis.VersionInput{RevisionID: otherRev.ID, Name: "x"}); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("another spec's revision: %v", err)
	}
	_ = other

	// Newest first, in pages.
	p1, err := f.mod.ListVersions(f.ctx, f.owner, spec.ID, workspaces.PageRequest{Limit: 2})
	if err != nil || len(p1.Items) != 2 || !p1.More || p1.Items[0].Name != "1.0.0+hotfix" || p1.Items[1].Name != "1.2.0-beta.1" {
		t.Fatalf("first page = %+v, %v", p1, err)
	}
	p2, err := f.mod.ListVersions(f.ctx, f.owner, spec.ID, workspaces.PageRequest{Limit: 2, After: p1.Items[1].ID})
	if err != nil || len(p2.Items) != 1 || p2.Items[0].Name != "1.0.0" {
		t.Errorf("second page = %+v, %v", p2, err)
	}
	if got, err := f.mod.GetVersion(f.ctx, f.owner, v1.ID); err != nil || got.Name != "1.0.0" {
		t.Errorf("get v1 = %+v, %v", got, err)
	}

	// Mocks find versions by name or as latest, without a user.
	if rev, content, err := f.mod.MockSource(f.ctx, spec.ID, "1.0.0"); err != nil || rev != first.ID || content != ordersV1 {
		t.Errorf("mock source of 1.0.0 = %v, %v", rev, err)
	}
	if rev, _, err := f.mod.MockSource(f.ctx, spec.ID, "LATEST"); err != nil || rev != old.RevisionID {
		t.Errorf("mock source of latest = %v, %v; want the newest version's revision", rev, err)
	}
	if _, _, err := f.mod.MockSource(f.ctx, spec.ID, "9.9.9"); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("an unknown version: %v", err)
	}

	// Viewers read but can't publish.
	viewer := f.member(t, "linus", workspaces.Viewer)
	if _, err := f.mod.PublishVersion(f.ctx, viewer, spec.ID, apis.VersionInput{Name: "9"}); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("a viewer publishes: %v", err)
	}
	if _, err := f.mod.GetVersion(f.ctx, viewer, v1.ID); err != nil {
		t.Errorf("a viewer can't read a version: %v", err)
	}
}

// ordersWarningsOnly breaks style rules but has no errors.
const ordersWarningsOnly = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.2.0
paths:
  /orders:
    get:
      responses:
        '200':
          description: The orders.
`

func TestPlacingServices(t *testing.T) {
	f := setup(t)
	viewer := f.member(t, "linus", workspaces.Viewer)
	// Without positions, services fill a grid four wide.
	var services []apis.Service
	for i := range 5 {
		s, err := f.mod.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: fmt.Sprintf("S%d", i), Slug: fmt.Sprintf("s%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, s)
	}
	if p := services[1].Position; p != (apis.Position{X: 288, Y: 0}) {
		t.Errorf("second service at %+v", p)
	}
	if p := services[4].Position; p != (apis.Position{X: 0, Y: 152}) {
		t.Errorf("fifth service at %+v", p)
	}

	moves := []apis.Placement{{ServiceID: services[0].ID, Position: apis.Position{X: 10, Y: 20}}, {ServiceID: services[1].ID, Position: apis.Position{X: -5, Y: 7.5}}}
	if err := f.mod.PlaceServices(f.ctx, f.owner, f.project, moves); err != nil {
		t.Fatal(err)
	}
	got, err := f.mod.GetService(f.ctx, f.owner, services[1].ID)
	// Moving isn't an edit: the version stays, so a rename in flight still applies.
	if err != nil || got.Position != (apis.Position{X: -5, Y: 7.5}) || got.Version != services[1].Version {
		t.Errorf("moved = %+v, %v", got, err)
	}

	if err := f.mod.PlaceServices(f.ctx, viewer, f.project, moves); !errors.Is(err, apis.ErrForbidden) {
		t.Errorf("a viewer moves services: %v", err)
	}
	// A service from another project undoes the whole move.
	other := append(moves[:1:1], apis.Placement{ServiceID: uuid.New(), Position: apis.Position{X: 1, Y: 1}})
	other[0].Position = apis.Position{X: 999, Y: 999}
	if err := f.mod.PlaceServices(f.ctx, f.owner, f.project, other); !errors.Is(err, apis.ErrNotFound) {
		t.Errorf("unknown service: %v", err)
	}
	if got, _ := f.mod.GetService(f.ctx, f.owner, services[0].ID); got.Position != (apis.Position{X: 10, Y: 20}) {
		t.Errorf("a failed move changed a position: %+v", got.Position)
	}
	var verrs validate.Errors
	twice := []apis.Placement{moves[0], moves[0]}
	if err := f.mod.PlaceServices(f.ctx, f.owner, f.project, twice); !errors.As(err, &verrs) || verrs[0].Pointer != "/positions/1/serviceId" {
		t.Errorf("a service twice: %v", err)
	}
	if err := f.mod.PlaceServices(f.ctx, f.owner, f.project, nil); !errors.As(err, &verrs) {
		t.Errorf("nothing to place: %v", err)
	}
}
