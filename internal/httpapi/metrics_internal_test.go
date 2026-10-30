// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

func TestMeasureRequestsByRoute(t *testing.T) {
	reg := metrics.NewRegistry()
	mux := http.NewServeMux()
	mux.Handle("/api/v1/projects/{projectId}", methods{http.MethodGet: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("projectId") == "missing" {
			w.WriteHeader(http.StatusNotFound)
		}
	})})
	h := measureRequests(newRequestMetrics(reg))(mux)
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/projects/prj_1"},
		{"GET", "/api/v1/projects/prj_2"},
		{"GET", "/api/v1/projects/missing"},
		{"BREW", "/api/v1/projects/prj_1"},
		{"GET", "/nowhere"},
	} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(req.method, req.path, nil))
	}
	var b bytes.Buffer
	reg.WriteTo(&b)
	got := b.String()
	for _, want := range []string{
		// Paths with IDs count under their pattern, not one series each.
		`signalbox_http_requests_total{route="/api/v1/projects/{projectId}",method="GET",code="200"} 2`,
		`signalbox_http_requests_total{route="/api/v1/projects/{projectId}",method="GET",code="404"} 1`,
		// Made-up methods don't make series of their own.
		`signalbox_http_requests_total{route="/api/v1/projects/{projectId}",method="other",code="405"} 1`,
		`signalbox_http_requests_total{route="unmatched",method="GET",code="404"} 1`,
		`signalbox_http_request_duration_seconds_count{route="/api/v1/projects/{projectId}",method="GET"} 3`,
		"signalbox_http_requests_in_flight 0",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "prj_1") || strings.Contains(got, "BREW") {
		t.Errorf("a path or method leaked into labels:\n%s", got)
	}
}
