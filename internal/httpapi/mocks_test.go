// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
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
		if resp.Header.Get("X-Frame-Options") != "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
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
