// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// router registers handlers by pattern, and names each request's span
// after its route, as OpenTelemetry's HTTP conventions ask.
type router struct{ mux *http.ServeMux }

func newRouter() *router { return &router{mux: http.NewServeMux()} }

func (rt *router) handle(pattern string, h http.HandlerFunc) {
	rt.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetName(r.Pattern)
		span.SetAttributes(attribute.String("http.route", routeOf(r.Pattern)))
		h(w, r)
	})
}

// routeOf drops the method from a pattern such as "GET /orders/{id}".
func routeOf(pattern string) string {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ' ' {
			return pattern[i+1:]
		}
	}
	return pattern
}

// serve runs a traced HTTP server on addr until ctx ends.
func serve(ctx context.Context, addr string, rt *router) error {
	srv := &http.Server{
		Addr: addr,
		// Health checks run every few seconds; their spans would drown the
		// shop's own.
		Handler: otelhttp.NewHandler(rt.mux, "server",
			otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/health" })),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr)
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// client calls other services, with the trace context in the headers.
var client = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: 10 * time.Second}

// errStatus is an answer with a status other than the one wanted.
type errStatus struct {
	status int
	body   string
}

func (e *errStatus) Error() string { return fmt.Sprintf("status %d: %s", e.status, e.body) }

// call sends a JSON request and decodes the JSON answer into out, which
// may be nil. An answer other than want is an *errStatus.
func call(ctx context.Context, method, url string, in, out any, want int) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return &errStatus{status: resp.StatusCode, body: string(b)}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// problem answers with an RFC 9457 problem, and marks the span failed
// when the server is at fault.
func problem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	if status >= 500 {
		span := trace.SpanFromContext(r.Context())
		span.RecordError(errors.New(detail))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": http.StatusText(status), "status": status, "detail": detail})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// check is one component of a health response.
type check struct {
	name string
	run  func(ctx context.Context) (status string, observed any, unit, output string)
}

// healthHandler answers in application/health+json, the format
// Signalbox's monitors read: pass, warn or fail, with each component.
func healthHandler(service string, checks ...check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		overall := "pass"
		components := map[string][]map[string]any{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, c := range checks {
			wg.Go(func() {
				status, observed, unit, output := c.run(ctx)
				entry := map[string]any{"status": status, "time": time.Now().UTC().Format(time.RFC3339)}
				if observed != nil {
					entry["observedValue"], entry["observedUnit"] = observed, unit
				}
				if output != "" {
					entry["output"] = output
				}
				mu.Lock()
				defer mu.Unlock()
				components[c.name] = append(components[c.name], entry)
				overall = worse(overall, status)
			})
		}
		wg.Wait()
		code := http.StatusOK
		if overall == "fail" {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/health+json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": overall, "serviceId": service, "version": version, "checks": components})
	}
}

func worse(a, b string) string {
	rank := map[string]int{"pass": 0, "warn": 1, "fail": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
