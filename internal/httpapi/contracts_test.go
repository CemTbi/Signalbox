// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/pkg/mockengine"
)

const contractSpec = `openapi: 3.1.0
info: {title: Orders API, version: 1.0.0}
tags: [{name: Orders}]
paths:
  /orders/{orderId}:
    get:
      tags: [Orders]
      summary: Get an order
      parameters:
        - {name: orderId, in: path, required: true, schema: {type: integer}, example: 42}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema:
                type: object
                required: [id, status]
                properties:
                  id: {type: integer}
                  status: {type: string, enum: [open, paid]}
`

func TestContractTestsRun(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Orders", "slug": "orders"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": contractSpec}}).decode(t, &spec)
	var v httpapi.VersionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).decode(t, &v)

	r := h.do(call{method: http.MethodPost, path: "/api/v1/versions/" + v.ID + "/contract-tests"})
	r.expect(t, http.StatusCreated, "")
	var made httpapi.ContractTestsJSON
	r.decode(t, &made)
	c := made.Collection
	if !made.Report.Created || made.Report.Requests != 1 || c.Contract == nil || c.Contract.VersionID != v.ID ||
		!strings.HasSuffix(c.Variables[0].Value, "/mocks/"+spec.ID+"/1.0.0") {
		t.Fatalf("made = %+v", made)
	}
	// Generating again updates the same collection.
	r = h.do(call{method: http.MethodPost, path: "/api/v1/versions/" + v.ID + "/contract-tests"})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &made)
	if made.Report.Created || made.Report.Replaced != 2 || made.Collection.ID != c.ID {
		t.Errorf("again = %+v", made.Report)
	}

	// Against the version's mock, the contract holds.
	mock, err := mockengine.New([]byte(contractSpec))
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(mock)
	t.Cleanup(api.Close)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "42", "status": "lost"}`))
	}))
	t.Cleanup(broken.Close)
	for _, tt := range []struct {
		base   string
		status string
		failed int
	}{
		{api.URL, "passed", 0},
		{broken.URL, "failed", 1},
	} {
		h.do(call{method: http.MethodPatch, path: "/api/v1/collections/" + c.ID, contentType: "application/merge-patch+json",
			body: map[string]any{"variables": []map[string]string{{"key": "baseUrl", "value": tt.base}, {"key": "orderId", "value": "42"}}}}).expect(t, http.StatusOK, "")
		var run httpapi.RunJSON
		h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + c.ID + "/runs", body: map[string]any{}}).decode(t, &run)
		deadline := time.Now().Add(30 * time.Second)
		for run.Status == "queued" || run.Status == "running" {
			if time.Now().After(deadline) {
				t.Fatalf("run still %s", run.Status)
			}
			time.Sleep(50 * time.Millisecond)
			h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID}).decode(t, &run)
		}
		var step httpapi.RunStepJSON
		h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/steps/1"}).decode(t, &step)
		if run.Status != tt.status || run.Totals.Tests != 2 || run.Totals.FailedTests != tt.failed || step.Path != "Orders" || step.Scripts.Tests[0].From != "Contract" {
			t.Errorf("%s: run = %s %+v, step = %+v", tt.base, run.Status, run.Totals, step.Scripts)
		}
		if tt.failed > 0 && !strings.Contains(step.Scripts.Tests[1].Error, "status") {
			t.Errorf("why = %q", step.Scripts.Tests[1].Error)
		}
	}

	// The saved request remembers its operation.
	var items struct {
		Data []httpapi.ItemJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/collections/" + c.ID + "/items"}).decode(t, &items)
	var get httpapi.ItemJSON
	for _, it := range items.Data {
		if it.Kind == "request" {
			h.do(call{method: http.MethodGet, path: "/api/v1/items/" + it.ID}).decode(t, &get)
		}
	}
	if get.Request == nil || get.Request.Contract == nil || get.Request.Contract.Path != "/orders/{orderId}" || get.Request.URL != "{{baseUrl}}/orders/{{orderId}}" {
		t.Errorf("item = %+v", get.Request)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/versions/ver_01j9z3v2x4z6b8d0f2h4k6m8p0/contract-tests"}).expect(t, http.StatusNotFound, "not-found")
}
