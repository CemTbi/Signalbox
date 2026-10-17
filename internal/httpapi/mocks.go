// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/pkg/mockengine"
)

// Hosted mocks answer as a published spec version describes, at
// /mocks/{specId}/{version}/..., where version is a version's name or
// "latest". They need no sign-in: frontends call them from their own
// origin, so they also allow any origin. See docs/adr/0021-hosted-mocks.md.

// MockSources finds the document a mock serves.
type MockSources interface {
	MockSource(ctx context.Context, specID uuid.UUID, name string) (uuid.UUID, string, error)
}

func (a *api) serveMock(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Expose-Headers", "Preference-Applied, Allow")
	// A CORS preflight, not a request to the mocked API.
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			h.Set("Access-Control-Allow-Headers", req)
		}
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if ok, retry := a.mocksByIP.Allow(clientIP(r, a.TrustedProxies).String()); !ok {
		writeRateLimited(w, r, retry)
		return
	}
	specID, err := id.Parse(id.Spec, r.PathValue("specId"))
	version := r.PathValue("version")
	if err != nil || version == "" {
		noSuchMock(w, r)
		return
	}
	revision, content, err := a.Mocks.MockSource(r.Context(), specID, version)
	if errors.Is(err, apis.ErrNotFound) {
		noSuchMock(w, r)
		return
	}
	if err != nil {
		a.fail(w, r, "mock", err)
		return
	}
	mock, err := a.mockCache.get(revision, func() (string, error) { return content, nil })
	if errors.Is(err, mockengine.ErrUnsupported) {
		writeProblem(w, r, http.StatusNotImplemented, "mock-unsupported",
			"This version is a Swagger 2.0 document. Mocks need OpenAPI 3.0 or later.")
		return
	}
	if errors.Is(err, mockengine.ErrTooLarge) {
		writeProblem(w, r, http.StatusUnprocessableEntity, "mock-too-large",
			"This version's schemas, with their references expanded, are too large to check requests against. Refer to shared schemas less often, or split the API.")
		return
	}
	if err != nil {
		a.Logger.WarnContext(r.Context(), "couldn't build a mock", "error", err, "request_id", RequestID(r.Context()))
		writeProblem(w, r, http.StatusUnprocessableEntity, "mock-failed",
			"The mock couldn't be built from this version: "+err.Error())
		return
	}
	// The mocked API's path is what follows the version.
	path := "/" + r.PathValue("path")
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	r2.RequestURI = path
	if r.URL.RawQuery != "" {
		r2.RequestURI += "?" + r.URL.RawQuery
	}
	// Mock responses come from specs anyone in a workspace can publish, on
	// Signalbox's own origin. They must never run as a page there: the CSP
	// sandboxes anything a browser renders into an origin of its own with
	// no scripts, and types a browser would render are downloads instead.
	h.Set("Content-Security-Policy", mockContentSecurityPolicy)
	mock.ServeHTTP(&inertResponse{ResponseWriter: w}, r2)
}

// mockContentSecurityPolicy allows a mock's response nothing when a browser
// shows it. API clients, such as a frontend's fetch, ignore it.
const mockContentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'; sandbox"

// inertResponse sends response types a browser could run, such as HTML
// and SVG, as downloads.
type inertResponse struct {
	http.ResponseWriter
	wrote bool
}

func (w *inertResponse) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		if activeContent(w.Header().Get("Content-Type")) {
			w.Header().Set("Content-Disposition", "attachment")
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *inertResponse) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// activeContent reports whether browsers may render a media type as a page
// or run script in it.
func activeContent(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType != ""
	}
	switch {
	case mt == "text/html", mt == "application/xhtml+xml", mt == "image/svg+xml", mt == "text/xml",
		mt == "application/xml", strings.HasSuffix(mt, "+xml"), mt == "text/xsl", mt == "application/xslt+xml":
		return true
	}
	return false
}

func noSuchMock(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, http.StatusNotFound, "no-such-mock",
		"There's no published version of a spec at this address. Publish a version, or check the spec ID and version name.")
}
