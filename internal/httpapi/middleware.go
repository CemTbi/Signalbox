// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

type contextKey int

const requestIDKey contextKey = iota

// RequestIDHeader carries the ID that ties a request to its log lines.
const RequestIDHeader = "X-Request-Id"

// An incoming ID is reused only if it can't inject anything into logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID returns the request's ID, or "" outside a request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			var b [12]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// recoverPanics turns a panic in a handler into a 500 problem response and
// an error log with the stack, instead of a dropped connection.
func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(p) // net/http aborts the response quietly
				}
				logger.ErrorContext(r.Context(), "handler panicked",
					"panic", p, "stack", string(debug.Stack()), "request_id", RequestID(r.Context()))
				writeProblem(w, r, http.StatusInternalServerError, "internal-error",
					"Something went wrong on the server. Quote the request ID when you report it.")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder remembers what a handler wrote, for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// actor is who made a request, for the access log. Authentication fills it
// in once it knows.
type actor struct {
	userID, tokenID uuid.UUID
}

type actorKey struct{}

// noteActor records who made the request, for the access log.
func noteActor(ctx context.Context, sess *identity.Session) {
	if a, ok := ctx.Value(actorKey{}).(*actor); ok {
		a.userID, a.tokenID = sess.User.ID, sess.AccessTokenID
	}
}

func logRequests(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			who := &actor{}
			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), actorKey{}, who)))

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case r.URL.Path == "/livez" || r.URL.Path == "/readyz":
				level = slog.LevelDebug // probes run every few seconds
			}
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
				"request_id", RequestID(r.Context()),
			}
			if who.userID != uuid.Nil {
				attrs = append(attrs, id.LogAttr("user_id", id.User, who.userID))
			}
			// A token's ID, never its value.
			if who.tokenID != uuid.Nil {
				attrs = append(attrs, id.LogAttr("token_id", id.AccessToken, who.tokenID))
			}
			logger.Log(r.Context(), level, "http request", attrs...)
		})
	}
}

// requestMetrics count the API's requests by route, for the ops listener's
// /metrics.
type requestMetrics struct {
	requests *metrics.Counter
	duration *metrics.Histogram
	inFlight *metrics.Gauge
}

func newRequestMetrics(r *metrics.Registry) requestMetrics {
	return requestMetrics{
		requests: r.NewCounter("signalbox_http_requests_total", "HTTP requests the api role answered, by route pattern, method and status code.",
			"route", "method", "code"),
		duration: r.NewHistogram("signalbox_http_request_duration_seconds", "How long the api role took to answer, by route pattern and method.",
			metrics.DurationBuckets, "route", "method"),
		inFlight: r.NewGauge("signalbox_http_requests_in_flight", "HTTP requests the api role is answering now."),
	}
}

// measured are the methods counted by name; others count as "other", so
// clients can't add series by inventing methods.
var measured = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// measureRequests counts requests by the route pattern that served them,
// such as /api/v1/projects/{projectId}, never by the path, whose IDs
// would make a series per resource. It runs inside logRequests, which
// records the status.
func measureRequests(m requestMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			m.inFlight.Inc()
			defer m.inFlight.Dec()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			// The mux sets the pattern on the request it was handed.
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			method := r.Method
			if !measured[method] {
				method = "other"
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			m.requests.Inc(route, method, strconv.Itoa(status))
			m.duration.Observe(time.Since(start).Seconds(), route, method)
		})
	}
}

// contentSecurityPolicy allows only the app's own scripts and styles. The
// editor will need worker-src blob: once Monaco lands.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// methods routes a path by HTTP method. Unlisted methods get a 405 problem
// response with an Allow header, so every API error has the same shape.
type methods map[string]http.Handler

func (m methods) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, ok := m[r.Method]
	if !ok && r.Method == http.MethodHead {
		h, ok = m[http.MethodGet]
	}
	if ok {
		h.ServeHTTP(w, r)
		return
	}
	allowed := make([]string, 0, len(m)+1)
	for method := range m {
		allowed = append(allowed, method)
	}
	if _, hasGet := m[http.MethodGet]; hasGet {
		allowed = append(allowed, http.MethodHead)
	}
	sort.Strings(allowed)
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeProblem(w, r, http.StatusMethodNotAllowed, "method-not-allowed",
		"This endpoint accepts "+strings.Join(allowed, ", ")+".")
}

func chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
