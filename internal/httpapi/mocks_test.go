// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

// mockCall requests a hosted mock as a frontend would: no session, from
// another origin. Mocks aren't part of api/openapi.yaml.
func (h *harness) mockCall(method, path string, headers map[string]string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestHostedMocks(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Orders", "slug": "orders"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": mockedSpec}}).decode(t, &spec)
	base := "/mocks/" + spec.ID

	// Nothing is published yet.
	if resp, body := h.mockCall(http.MethodGet, base+"/latest/orders/42", nil); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "no-such-mock") {
		t.Errorf("before publishing: %d %s", resp.StatusCode, body)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).expect(t, http.StatusCreated, "")

	for _, version := range []string{"1.0.0", "latest"} {
		resp, body := h.mockCall(http.MethodGet, base+"/"+version+"/orders/42", nil)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.HasPrefix(body, "{") {
			t.Errorf("GET %s/orders/42 = %d %v %s", version, resp.StatusCode, resp.Header, body)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") ||
			resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("security headers on a mock: %v", resp.Header)
		}
	}
	// The mock checks requests: id is an integer.
	if resp, body := h.mockCall(http.MethodGet, base+"/1.0.0/orders/abc", nil); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "path parameter id") {
		t.Errorf("invalid request: %d %s", resp.StatusCode, body)
	}
	// A browser's preflight.
	resp, _ := h.mockCall(http.MethodOptions, base+"/1.0.0/orders", map[string]string{
		"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type, prefer",
	})
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Headers") != "content-type, prefer" {
		t.Errorf("preflight = %d %v", resp.StatusCode, resp.Header)
	}
	// Unknown versions and malformed IDs.
	for _, path := range []string{base + "/9.9.9/orders/1", "/mocks/spec_nope/1.0.0/orders/1", base} {
		if resp, body := h.mockCall(http.MethodGet, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d %s", path, resp.StatusCode, body)
		}
	}
}

// mockedSpec describes a response body and an integer ID, for the mock to
// generate and check.
const mockedSpec = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.0.0
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema:
                type: object
                required: [id, total]
                properties:
                  id: {type: integer}
                  total: {type: number}
  /orders:
    post:
      operationId: createOrder
      responses:
        '201':
          description: Created.
`

// A published spec mustn't be able to run script on Signalbox's origin
// through its mock: anyone in a workspace can publish one.
func TestMocksCantServeActivePages(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Pages", "slug": "pages"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Pages", "content": htmlSpec}}).decode(t, &spec)
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).expect(t, http.StatusCreated, "")

	for _, path := range []string{"/page", "/image"} {
		resp, body := h.mockCall(http.MethodGet, "/mocks/"+spec.ID+"/latest"+path, nil)
		// XML types are encoded as XML, so only the HTML example keeps its tag.
		if resp.StatusCode != http.StatusOK || (path == "/page" && !strings.Contains(body, "<script>")) {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, body)
		}
		if resp.Header.Get("Content-Disposition") != "attachment" {
			t.Errorf("GET %s: Content-Disposition = %q, want a download", path, resp.Header.Get("Content-Disposition"))
		}
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "default-src 'none'; frame-ancestors 'none'; sandbox" {
			t.Errorf("GET %s: CSP = %q", path, csp)
		}
	}
	// JSON stays an ordinary response.
	resp, _ := h.mockCall(http.MethodGet, "/mocks/"+spec.ID+"/latest/data", nil)
	if resp.Header.Get("Content-Disposition") != "" {
		t.Errorf("JSON: Content-Disposition = %q", resp.Header.Get("Content-Disposition"))
	}
}

const htmlSpec = `openapi: 3.1.1
info:
  title: Pages
  version: 1.0.0
paths:
  /page:
    get:
      responses:
        '200':
          description: A page.
          content:
            text/html:
              example: "<html><script>fetch('/api/v1/me')</script></html>"
  /image:
    get:
      responses:
        '200':
          description: An image.
          content:
            image/svg+xml:
              example: "<svg xmlns='http://www.w3.org/2000/svg'><script>alert(1)</script></svg>"
  /data:
    get:
      responses:
        '200':
          description: Data.
          content:
            application/json:
              example: {ok: true}
`

// A version whose schemas expand too far to check requests against is
// refused, instead of keeping the server busy building its mock.
func TestMocksTooLargeToBuild(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Deep", "slug": "deep"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Deep", "content": doublingSpec(30)}}).decode(t, &spec)
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).expect(t, http.StatusCreated, "")

	resp, body := h.mockCall(http.MethodGet, "/mocks/"+spec.ID+"/latest/a", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `"mock-too-large"`) {
		t.Errorf("GET /a = %d %s", resp.StatusCode, body)
	}
}

// doublingSpec is a document whose schema doubles at each level: each
// level refers to the next twice.
func doublingSpec(levels int) string {
	var doc strings.Builder
	doc.WriteString(`openapi: 3.1.1
info: {title: Deep, version: 1.0.0, description: Deep schemas.}
servers: [{url: 'https://deep.example.com'}]
tags: [{name: deep, description: Deep.}]
paths:
  /a:
    get:
      operationId: getA
      summary: Get A.
      description: Gets A.
      tags: [deep]
      responses:
        '200':
          description: A.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/L0'}
components:
  schemas:
`)
	for i := range levels {
		next := "{type: string}"
		if i < levels-1 {
			next = fmt.Sprintf("{$ref: '#/components/schemas/L%d'}", i+1)
		}
		fmt.Fprintf(&doc, `    L%d:
      type: object
      properties:
        left: %s
        right: %s
`, i, next, next)
	}
	return doc.String()
}
