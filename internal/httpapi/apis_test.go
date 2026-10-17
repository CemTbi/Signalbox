// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

const ordersSpec = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.0.0
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        '200':
          description: The order.
`

// newProject creates a workspace and a project in it as the signed-in user.
func (h *harness) newProject(slug string) httpapi.ProjectJSON {
	h.t.Helper()
	ws := h.newWorkspace("Team "+slug, slug)
	var p httpapi.ProjectJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/workspaces/" + ws.ID + "/projects", body: map[string]string{"name": "Checkout"}})
	r.expect(h.t, http.StatusCreated, "")
	r.decode(h.t, &p)
	return p
}

func TestServicesAndSpecs(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")

	// A service on the canvas.
	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]any{
		"name": "Orders API", "slug": "orders-api", "telemetryServiceName": "orders", "position": map[string]float64{"x": 320, "y": 120},
	}})
	r.expect(t, http.StatusCreated, "")
	var svc httpapi.ServiceJSON
	r.decode(t, &svc)
	if svc.Position.X != 320 || r.header.Get("Location") != "/api/v1/services/"+svc.ID {
		t.Errorf("service = %+v, Location %q", svc, r.header.Get("Location"))
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services",
		body: map[string]any{"name": "Again", "slug": "orders-api"}}).expect(t, http.StatusConflict, "slug-taken")

	// Moving it, with If-Match.
	tag := r.header.Get("ETag")
	r = h.do(call{method: http.MethodPatch, path: "/api/v1/services/" + svc.ID, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": tag}, body: map[string]any{"position": map[string]float64{"x": 480, "y": 200}}})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &svc)
	if svc.Position.X != 480 || svc.Name != "Orders API" {
		t.Errorf("moved service = %+v", svc)
	}
	h.do(call{method: http.MethodPatch, path: "/api/v1/services/" + svc.ID, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": tag}, body: map[string]any{"name": "Stale"}}).expect(t, http.StatusPreconditionFailed, "edit-conflict")
	h.do(call{method: http.MethodPatch, path: "/api/v1/services/" + svc.ID, contentType: "application/merge-patch+json",
		body: map[string]any{"position": map[string]any{"x": 1}}, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	// Its spec, with the first revision.
	r = h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": ordersSpec}})
	r.expect(t, http.StatusCreated, "")
	var spec httpapi.SpecJSON
	r.decode(t, &spec)
	specTag := r.header.Get("ETag")
	var page struct {
		Data []httpapi.SpecJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/services/" + svc.ID + "/specs"}).decode(t, &page)
	if len(page.Data) != 1 || page.Data[0].CurrentRevisionID != spec.CurrentRevisionID {
		t.Errorf("specs = %+v", page)
	}

	// Saving needs the spec's ETag.
	v2 := strings.Replace(ordersSpec, "version: 1.0.0", "version: 1.1.0", 1)
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/revisions",
		body: map[string]string{"content": v2}, invalid: true}).expect(t, http.StatusPreconditionRequired, "precondition-required")
	r = h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/revisions", headers: map[string]string{"If-Match": specTag},
		body: map[string]string{"content": v2, "message": "Bump"}})
	r.expect(t, http.StatusCreated, "")
	var rev httpapi.RevisionJSON
	r.decode(t, &rev)
	if rev.Number != 2 || rev.APIVersion != "1.1.0" || rev.Title != "Orders API" || rev.Format != "openapi-3.1" || len(rev.SHA256) != 64 {
		t.Errorf("revision = %+v", rev)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/revisions", headers: map[string]string{"If-Match": specTag},
		body: map[string]string{"content": ordersSpec}}).expect(t, http.StatusPreconditionFailed, "edit-conflict")
	newTag := r.header.Get("ETag")

	// A document that isn't OpenAPI is refused and explained.
	r = h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/revisions", headers: map[string]string{"If-Match": newTag},
		body: map[string]string{"content": "hello: world\n"}})
	r.expect(t, http.StatusBadRequest, "unreadable-document")

	// History, content and report.
	var revs struct {
		Data []httpapi.RevisionJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/specs/" + spec.ID + "/revisions"}).decode(t, &revs)
	if len(revs.Data) != 2 || revs.Data[0].Number != 2 {
		t.Errorf("revisions = %+v", revs)
	}
	r = h.do(call{method: http.MethodGet, path: "/api/v1/revisions/" + spec.CurrentRevisionID + "/content"})
	r.expect(t, http.StatusOK, "")
	if string(r.body) != ordersSpec || !strings.HasPrefix(r.header.Get("Content-Type"), "application/yaml") ||
		!strings.Contains(r.header.Get("Cache-Control"), "immutable") {
		t.Errorf("content = %q, %v", r.body, r.header)
	}
	var report struct {
		Info   struct{ Title, Version string } `json:"info"`
		Counts struct{ Errors int }            `json:"counts"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/revisions/" + rev.ID + "/report"}).decode(t, &report)
	if report.Info.Version != "1.1.0" || report.Counts.Errors != 0 {
		t.Errorf("report = %+v", report)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/revisions/" + rev.ID}).expect(t, http.StatusOK, "")
	h.do(call{method: http.MethodGet, path: "/api/v1/revisions/rev_nope", invalid: true}).expect(t, http.StatusNotFound, "not-found")

	// Rename the spec, then delete it and the service.
	r = h.do(call{method: http.MethodPatch, path: "/api/v1/specs/" + spec.ID, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": newTag}, body: map[string]string{"name": "Orders"}})
	r.expect(t, http.StatusOK, "")
	h.do(call{method: http.MethodDelete, path: "/api/v1/specs/" + spec.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodDelete, path: "/api/v1/services/" + svc.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: "/api/v1/services/" + svc.ID}).expect(t, http.StatusNotFound, "not-found")
}

func TestPublishingVersions(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Orders", "slug": "orders"}}).decode(t, &svc)
	r := h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": ordersSpec}})
	r.expect(t, http.StatusCreated, "")
	var spec httpapi.SpecJSON
	r.decode(t, &spec)
	specTag := r.header.Get("ETag")

	// The current revision, named after its info.version.
	r = h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{"notes": "First release"}})
	r.expect(t, http.StatusCreated, "")
	var v httpapi.VersionJSON
	r.decode(t, &v)
	if v.Name != "1.0.0" || v.RevisionID != spec.CurrentRevisionID || r.header.Get("Location") != "/api/v1/versions/"+v.ID {
		t.Errorf("version = %+v, Location %q", v, r.header.Get("Location"))
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).expect(t, http.StatusConflict, "version-exists")

	// A revision with errors can't be published.
	broken := strings.Replace(ordersSpec, "          description: The order.\n", "          $ref: '#/components/responses/Missing'\n", 1)
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/revisions", headers: map[string]string{"If-Match": specTag},
		body: map[string]string{"content": broken}}).expect(t, http.StatusCreated, "")
	r = h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{"name": "1.1.0"}})
	r.expect(t, http.StatusUnprocessableEntity, "spec-has-errors")

	var page struct {
		Data []httpapi.VersionJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/specs/" + spec.ID + "/versions"}).decode(t, &page)
	if len(page.Data) != 1 || page.Data[0].ID != v.ID {
		t.Errorf("versions = %+v", page)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/versions/" + v.ID}).expect(t, http.StatusOK, "")
}
