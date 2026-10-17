// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestReferenceDocs(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Orders", "slug": "orders"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": ordersSpec}}).decode(t, &spec)
	var v httpapi.VersionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).decode(t, &v)

	r := h.do(call{method: http.MethodGet, path: "/api/v1/versions/" + v.ID + "/reference"})
	r.expect(t, http.StatusOK, "")
	var ref struct {
		Title  string `json:"title"`
		Groups []struct {
			Operations []struct {
				Method, Path string
			} `json:"operations"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(r.body, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Title != "Orders API" || len(ref.Groups) == 0 || len(ref.Groups[0].Operations) == 0 {
		t.Errorf("reference = %s", r.body)
	}
	// The version's revision tags it; a client that has it gets 304.
	tag := r.header.Get("ETag")
	if tag != `"`+v.RevisionID+`"` {
		t.Errorf("ETag = %q", tag)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/versions/" + v.ID + "/reference", headers: map[string]string{"If-None-Match": tag}}).
		expect(t, http.StatusNotModified, "")

	// Someone outside the workspace can't tell the version exists.
	h.createUser("Mallory", "mallory@example.com")
	h.signOutLocally()
	h.signIn("mallory@example.com")
	h.do(call{method: http.MethodGet, path: "/api/v1/versions/" + v.ID + "/reference"}).expect(t, http.StatusNotFound, "not-found")
}

// Swagger 2.0 versions can be published, but have no reference docs yet.
func TestReferenceDocsOfSwagger2(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("legacy")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Legacy", "slug": "legacy"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Legacy", "content": swagger2}}).decode(t, &spec)
	var v httpapi.VersionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).decode(t, &v)
	h.do(call{method: http.MethodGet, path: "/api/v1/versions/" + v.ID + "/reference"}).expect(t, http.StatusNotImplemented, "reference-unsupported")
}

const swagger2 = `swagger: '2.0'
info:
  title: Legacy
  version: '1.0'
basePath: /v1
paths:
  /ping:
    get:
      operationId: ping
      responses:
        '200':
          description: Pong.
`
