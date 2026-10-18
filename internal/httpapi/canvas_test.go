// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestPlacingServicesOnTheCanvas(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var a, b httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "A", "slug": "a"}}).decode(t, &a)
	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "B", "slug": "b"}})
	r.decode(t, &b)
	tagB := r.header.Get("ETag")
	if b.Position.X != 288 || b.Position.Y != 0 {
		t.Errorf("B was placed at %+v, want the next grid spot", b.Position)
	}

	canvas := "/api/v1/projects/" + p.ID + "/canvas"
	h.do(call{method: http.MethodPut, path: canvas, body: map[string]any{"positions": []map[string]any{
		{"serviceId": a.ID, "x": 40, "y": 80}, {"serviceId": b.ID, "x": 400, "y": 80},
	}}}).expect(t, http.StatusNoContent, "")
	r = h.do(call{method: http.MethodGet, path: "/api/v1/services/" + b.ID})
	var moved httpapi.ServiceJSON
	r.decode(t, &moved)
	// The move kept B's ETag, so an edit begun before it still applies.
	if moved.Position.X != 400 || r.header.Get("ETag") != tagB {
		t.Errorf("B = %+v, ETag %q (was %q)", moved.Position, r.header.Get("ETag"), tagB)
	}
	h.do(call{method: http.MethodPatch, path: "/api/v1/services/" + b.ID, contentType: "application/merge-patch+json",
		headers: map[string]string{"If-Match": tagB}, body: map[string]string{"name": "B2"}}).expect(t, http.StatusOK, "")

	h.do(call{method: http.MethodPut, path: canvas, invalid: true, body: map[string]any{"positions": []map[string]any{{"serviceId": "nope", "x": 1, "y": 1}}}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPut, path: canvas, body: map[string]any{"positions": []map[string]any{{"serviceId": "svc_01j9z3q1s3v5w7y9a1c3e5g7j9", "x": 1, "y": 1}}}}).
		expect(t, http.StatusNotFound, "not-found")
}
