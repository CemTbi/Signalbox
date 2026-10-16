// SPDX-License-Identifier: Apache-2.0

// Package httpapi serves Signalbox's public REST API and web app for the api
// role. The API contract lives in api/openapi.yaml; see
// docs/adr/0005-http-api-conventions.md.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// problemMediaType is the media type of RFC 9457 problem details.
const problemMediaType = "application/problem+json"

// Problem is an RFC 9457 problem details object. Every error the API returns
// uses it.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	// Code is a stable, machine-readable identifier for the problem.
	Code string `json:"code,omitempty"`
	// RequestID matches the X-Request-Id response header and the server logs.
	RequestID string `json:"requestId,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Instance:  r.URL.Path,
		Code:      code,
		RequestID: RequestID(r.Context()),
	}
	w.Header().Set("Content-Type", problemMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, http.StatusNotFound, "not-found", "There is no API endpoint at this path.")
}
