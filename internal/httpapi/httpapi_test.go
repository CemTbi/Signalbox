// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/roles"
	"github.com/CemTbi/Signalbox/pkg/specengine"
)

func newServer(t *testing.T, maxSpecBytes int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(httpapi.NewHandler(httpapi.Deps{
		Logger:       slog.New(slog.DiscardHandler),
		Health:       health.New("signalbox-server", "test"),
		Build:        buildinfo.Info{Version: "1.2.3"},
		Roles:        []roles.Name{roles.API},
		MaxSpecBytes: maxSpecBytes,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func decodeProblem(t *testing.T, b []byte) httpapi.Problem {
	t.Helper()
	var p httpapi.Problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("body is not a problem document: %v\n%s", err, b)
	}
	return p
}

func TestMeta(t *testing.T) {
	srv := newServer(t, 0)
	resp, body := do(t, http.MethodGet, srv.URL+"/api/v1/meta", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var meta httpapi.Meta
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Product != "Signalbox" || meta.Version != "1.2.3" || meta.APIVersion != "v1" {
		t.Errorf("meta = %+v", meta)
	}
	if resp.Header.Get(httpapi.RequestIDHeader) == "" {
		t.Error("response has no request ID")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("security headers are missing")
	}
}

func TestRequestIDIsReusedOnlyWhenSafe(t *testing.T) {
	srv := newServer(t, 0)
	for sent, wantSame := range map[string]bool{"abc-123": true, "bad id\r\ninjected": false} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/meta", nil)
		req.Header.Set(httpapi.RequestIDHeader, sent)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// net/http refuses to send header values with line breaks.
			if !wantSame {
				continue
			}
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get(httpapi.RequestIDHeader); (got == sent) != wantSame {
			t.Errorf("sent %q, got back %q", sent, got)
		}
	}
}

func TestErrorsAreProblemDocuments(t *testing.T) {
	srv := newServer(t, 0)
	tests := []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodGet, "/api/v1/nope", http.StatusNotFound, "not-found"},
		{http.MethodDelete, "/api/v1/meta", http.StatusMethodNotAllowed, "method-not-allowed"},
		{http.MethodGet, "/", http.StatusNotFound, "not-found"},
	}
	for _, tt := range tests {
		resp, body := do(t, tt.method, srv.URL+tt.path, "")
		if resp.StatusCode != tt.status || resp.Header.Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s = %d %s", tt.method, tt.path, resp.StatusCode, resp.Header.Get("Content-Type"))
			continue
		}
		p := decodeProblem(t, body)
		if p.Code != tt.code || p.Status != tt.status || p.RequestID == "" {
			t.Errorf("%s %s problem = %+v", tt.method, tt.path, p)
		}
	}
	resp, _ := do(t, http.MethodDelete, srv.URL+"/api/v1/meta", "")
	if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
}

func TestSpecChecks(t *testing.T) {
	srv := newServer(t, 0)
	spec, err := os.ReadFile(filepath.Join("..", "..", "pkg", "specengine", "testdata", "unresolved-ref.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, http.MethodPost, srv.URL+"/api/v1/spec-checks", string(spec))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var report specengine.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Format != specengine.FormatOpenAPI31 || report.Counts.Errors != 1 || report.Counts.Warnings == 0 {
		t.Errorf("report = %+v", report.Counts)
	}

	_, body = do(t, http.MethodPost, srv.URL+"/api/v1/spec-checks?ruleSets=none", string(spec))
	_ = json.Unmarshal(body, &report)
	if report.Counts.Warnings != 0 || report.Counts.Errors != 1 {
		t.Errorf("ruleSets=none still linted: %+v", report.Counts)
	}
}

func TestSpecChecksRejectsBadRequests(t *testing.T) {
	srv := newServer(t, 1024)
	tests := []struct {
		name, query, body string
		status            int
		code              string
	}{
		{"empty body", "", "   ", http.StatusBadRequest, "empty-body"},
		{"too large", "", "openapi: 3.1.0\n" + strings.Repeat("# padding\n", 200), http.StatusRequestEntityTooLarge, "document-too-large"},
		{"unknown rule set", "?ruleSets=strict", "openapi: 3.1.0", http.StatusBadRequest, "invalid-parameter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, http.MethodPost, srv.URL+"/api/v1/spec-checks"+tt.query, tt.body)
			if resp.StatusCode != tt.status || decodeProblem(t, body).Code != tt.code {
				t.Errorf("got %d %s, want %d %s", resp.StatusCode, body, tt.status, tt.code)
			}
		})
	}
}

func TestSpecChecksNeverFollowsFileReferences(t *testing.T) {
	srv := newServer(t, 0)
	spec := `openapi: 3.1.0
info: {title: Probe, version: 1.0.0}
paths:
  /x:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: {$ref: './secrets.yaml#/Secret'}
`
	_, body := do(t, http.MethodPost, srv.URL+"/api/v1/spec-checks?ruleSets=none", spec)
	var report specengine.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Counts.Errors == 0 {
		t.Errorf("a file $ref resolved on the server: %+v", report)
	}
}
