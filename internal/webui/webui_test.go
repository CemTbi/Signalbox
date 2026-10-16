// SPDX-License-Identifier: Apache-2.0

package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CemTbi/Signalbox/internal/webui"
)

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestServesSinglePageApp(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app-1a2b.js": {Data: []byte("console.log(1)")},
		"favicon.svg":        {Data: []byte("<svg/>")},
	})

	tests := []struct {
		target, wantBody, wantCache string
		wantCode                    int
	}{
		{"/", "<div id=root>", "no-cache", http.StatusOK},
		{"/projects/prj_123/canvas", "<div id=root>", "no-cache", http.StatusOK},
		{"/assets/app-1a2b.js", "console.log", "public, max-age=31536000, immutable", http.StatusOK},
		{"/favicon.svg", "<svg/>", "no-cache", http.StatusOK},
		{"/assets/missing.js", "404", "", http.StatusNotFound},
		{"/../../etc/passwd", "<div id=root>", "no-cache", http.StatusOK},
	}
	for _, tt := range tests {
		rec := get(h, tt.target)
		if rec.Code != tt.wantCode || !strings.Contains(rec.Body.String(), tt.wantBody) {
			t.Errorf("GET %s = %d %q, want %d containing %q", tt.target, rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
		}
		if got := rec.Header().Get("Cache-Control"); tt.wantCache != "" && got != tt.wantCache {
			t.Errorf("GET %s Cache-Control = %q, want %q", tt.target, got, tt.wantCache)
		}
	}
}

func TestExplainsMissingBuild(t *testing.T) {
	rec := get(webui.NewHandler(fstest.MapFS{}), "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "built without the web app") {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRejectsWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	webui.NewHandler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", rec.Code)
	}
}
