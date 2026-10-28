// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestRunningACollection(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/orders" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		fmt.Fprintf(w, `{"user":%q}`, r.URL.Query().Get("user"))
	}))
	t.Cleanup(shop.Close)

	var col httpapi.CollectionJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/collections", body: map[string]string{"name": "Shop API"}})
	r.expect(t, http.StatusCreated, "")
	r.decode(t, &col)
	for _, it := range []map[string]any{
		{"kind": "request", "name": "Who am I", "request": map[string]any{"method": "GET", "url": shop.URL + "/me?user={{user}}"},
			"scripts": map[string]string{"test": `pm.test("is me", () => pm.expect(pm.response.json().user).to.eql(pm.iterationData.get("user")));`}},
		{"kind": "request", "name": "Orders", "request": map[string]any{"method": "GET", "url": shop.URL + "/orders"},
			"scripts": map[string]string{"test": `console.log("orders"); pm.test("ok", () => pm.response.to.have.status(200));`}},
	} {
		h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + col.ID + "/items", body: it}).expect(t, http.StatusCreated, "")
	}
	runs := "/api/v1/collections/" + col.ID + "/runs"

	r = h.do(call{method: http.MethodPost, path: runs, body: map[string]any{"data": map[string]string{"format": "csv", "content": "user\nada\nbob\n"}}})
	r.expect(t, http.StatusAccepted, "")
	var run httpapi.RunJSON
	r.decode(t, &run)
	if run.Status != "queued" || run.Iterations != 2 || r.header.Get("Location") != "/api/v1/runs/"+run.ID {
		t.Errorf("started = %+v", run)
	}
	deadline := time.Now().Add(30 * time.Second)
	for run.Status == "queued" || run.Status == "running" {
		if time.Now().After(deadline) {
			t.Fatalf("run still %s", run.Status)
		}
		time.Sleep(50 * time.Millisecond)
		h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID}).decode(t, &run)
	}
	if run.Status != "failed" || run.Totals.Requests != 4 || run.Totals.FailedTests != 2 || run.Totals.Tests != 4 || run.FinishedAt == "" {
		t.Errorf("finished = %+v", run)
	}

	var steps httpapi.RunStepsJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/steps?after=1&limit=2"}).decode(t, &steps)
	if len(steps.Data) != 2 || !steps.More || steps.Data[0].Seq != 2 || steps.Data[0].Name != "Orders" || !steps.Data[0].Failed {
		t.Errorf("steps = %+v", steps)
	}
	var step httpapi.RunStepJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/steps/2"}).decode(t, &step)
	if step.Response == nil || step.Response.Status != 500 || !strings.Contains(step.Response.Body, "user") || len(step.Scripts.Logs) != 1 {
		t.Errorf("step 2 = %+v", step)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/steps/99"}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/steps?after=-1", invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	r = h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/report?format=junit"})
	r.expect(t, http.StatusOK, "")
	var suites struct {
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(r.body, &suites); err != nil || suites.Failures != 2 {
		t.Errorf("junit = %d failures, %v", suites.Failures, err)
	}
	if cd := r.header.Get("Content-Disposition"); cd != `attachment; filename="shop-api-`+run.ID+`.xml"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	r = h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/report?format=html"})
	r.expect(t, http.StatusOK, "")
	if !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") || !strings.Contains(r.header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("html headers = %v", r.header)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID + "/report?format=pdf", invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	// Cancelling a finished run changes nothing.
	r = h.do(call{method: http.MethodPost, path: "/api/v1/runs/" + run.ID + "/cancel"})
	r.expect(t, http.StatusOK, "")
	var canceled httpapi.RunJSON
	r.decode(t, &canceled)
	if canceled.Status != "failed" {
		t.Errorf("cancel finished = %s", canceled.Status)
	}
	var list struct {
		Data []httpapi.RunJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: runs}).decode(t, &list)
	if len(list.Data) != 1 || list.Data[0].ID != run.ID {
		t.Errorf("list = %+v", list)
	}

	// What can't run says why.
	for _, tt := range []struct {
		body    map[string]any
		pointer string
		// invalid marks bodies the contract itself rejects.
		invalid bool
	}{
		{map[string]any{"data": map[string]string{"format": "csv", "content": "user\n"}}, "/data/content", false},
		{map[string]any{"data": map[string]string{"format": "xml", "content": "<a/>"}}, "/data/format", true},
		{map[string]any{"iterations": 1001}, "/iterations", true},
		{map[string]any{"delayMs": 70000}, "/delayMs", true},
		{map[string]any{"folderId": "col_01j9z3w5y7a9c1e3g5j7m9p1r3"}, "/folderId", true},
	} {
		r := h.do(call{method: http.MethodPost, path: runs, body: tt.body, invalid: tt.invalid})
		r.expect(t, http.StatusBadRequest, "validation-failed")
		if !strings.Contains(string(r.body), `"pointer":"`+tt.pointer+`"`) {
			t.Errorf("%v: %s", tt.body, r.body)
		}
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/run_01j9z3w5y7a9c1e3g5j7m9p1r3"}).expect(t, http.StatusNotFound, "not-found")
}
