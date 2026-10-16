// SPDX-License-Identifier: Apache-2.0

// Package httpapi serves Signalbox's public REST API and web app for the api
// role. The API contract lives in api/openapi.yaml; see
// docs/adr/0005-http-api-conventions.md.
package httpapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/CemTbi/Signalbox/internal/platform/validate"
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
	// Errors lists the invalid fields of a request body.
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError is one invalid field in a request body.
type FieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
	Code    string `json:"code,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	writeProblemDoc(w, r, Problem{Status: status, Code: code, Detail: detail})
}

func writeProblemDoc(w http.ResponseWriter, r *http.Request, p Problem) {
	p.Type = "about:blank"
	p.Title = http.StatusText(p.Status)
	p.Instance = r.URL.Path
	p.RequestID = RequestID(r.Context())
	w.Header().Set("Content-Type", problemMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeInvalid answers 400 with every invalid field.
func writeInvalid(w http.ResponseWriter, r *http.Request, fields validate.Errors) {
	p := Problem{Status: http.StatusBadRequest, Code: "validation-failed"}
	if len(fields) == 1 {
		p.Detail = "The request has 1 invalid field."
	} else {
		p.Detail = fmt.Sprintf("The request has %d invalid fields.", len(fields))
	}
	for _, f := range fields {
		p.Errors = append(p.Errors, FieldError{Pointer: f.Pointer, Detail: f.Detail, Code: f.Code})
	}
	writeProblemDoc(w, r, p)
}

// writeRateLimited answers 429 with Retry-After in whole seconds, rounded up.
func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	secs := max(1, int(math.Ceil(retryAfter.Seconds())))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeProblem(w, r, http.StatusTooManyRequests, "rate-limited",
		fmt.Sprintf("Too many attempts. Try again in %d seconds.", secs))
}

func writeInternalError(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, http.StatusInternalServerError, "internal-error",
		"Something went wrong on the server. Quote the request ID when you report it.")
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
