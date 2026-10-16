// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/roles"
)

// DefaultMaxSpecBytes bounds the size of a document sent for checking.
const DefaultMaxSpecBytes = 10 << 20

// Deps is everything the api role's handlers need.
type Deps struct {
	Logger *slog.Logger
	Health *health.Checker
	Build  buildinfo.Info
	Roles  []roles.Name
	// UI serves the web app. Nil serves 404s outside /api.
	UI http.Handler
	// MaxSpecBytes bounds request bodies that carry an OpenAPI document.
	MaxSpecBytes int64
}

// NewHandler returns the api role's HTTP handler.
func NewHandler(d Deps) http.Handler {
	if d.MaxSpecBytes <= 0 {
		d.MaxSpecBytes = DefaultMaxSpecBytes
	}
	mux := http.NewServeMux()
	mux.Handle("/livez", methods{http.MethodGet: d.Health.LiveHandler()})
	mux.Handle("/readyz", methods{http.MethodGet: d.Health.ReadyHandler()})
	mux.Handle("/api/v1/meta", methods{http.MethodGet: metaHandler(d)})
	mux.Handle("/api/v1/spec-checks", methods{http.MethodPost: specChecksHandler(d)})
	mux.HandleFunc("/api/", notFound)
	if d.UI != nil {
		mux.Handle("/", d.UI)
	} else {
		mux.HandleFunc("/", notFound)
	}
	return chain(mux,
		withRequestID,
		logRequests(d.Logger),
		recoverPanics(d.Logger),
		securityHeaders,
	)
}
