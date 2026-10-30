// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"google.golang.org/grpc"
)

// version is the shop's version, sent with its spans.
const version = "1.0.0"

// startTracing sends the service's spans to Signalbox, or nowhere when no
// endpoint is set. The exporter reads OTEL_EXPORTER_OTLP_ENDPOINT and the
// other standard settings, and OTEL_EXPORTER_OTLP_PROTOCOL picks grpc (the
// default) or http/protobuf.
//
// The ingest key is SIGNALBOX_INGEST_KEY, or read from the file in
// SIGNALBOX_INGEST_KEY_FILE for every export, so the services can start
// before the seed writes it and follow it when the seed replaces it.
func startTracing(ctx context.Context, service string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		slog.Info("OTEL_EXPORTER_OTLP_ENDPOINT isn't set, so spans go nowhere")
		return func(context.Context) error { return nil }, nil
	}
	key := &keySource{fixed: os.Getenv("SIGNALBOX_INGEST_KEY"), path: os.Getenv("SIGNALBOX_INGEST_KEY_FILE")}
	if key.fixed == "" && key.path == "" {
		return nil, fmt.Errorf("set SIGNALBOX_INGEST_KEY, or SIGNALBOX_INGEST_KEY_FILE to a file that holds it")
	}

	var (
		exporter sdktrace.SpanExporter
		err      error
	)
	switch protocol := env("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"); protocol {
	case "grpc":
		exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithDialOption(grpc.WithPerRPCCredentials(key)))
	case "http/protobuf":
		exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithHTTPClient(&http.Client{
			Transport: keyTransport{key: key, next: http.DefaultTransport}, Timeout: 10 * time.Second,
		}))
	default:
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_PROTOCOL %q: use grpc or http/protobuf", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("make the span exporter: %w", err)
	}
	res := resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service),
		semconv.ServiceVersion(version),
		semconv.ServiceNamespace("demo-shop"),
		semconv.DeploymentEnvironmentName(env("DEMO_ENVIRONMENT", "production")),
	)
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// keySource reads the ingest key, from the file again when it changes.
type keySource struct {
	fixed, path string

	mu       sync.Mutex
	key      string
	modified time.Time
}

func (k *keySource) get() string {
	if k.fixed != "" {
		return k.fixed
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	info, err := os.Stat(k.path)
	if err != nil {
		return ""
	}
	if !info.ModTime().Equal(k.modified) {
		b, err := os.ReadFile(k.path)
		if err != nil {
			return k.key
		}
		k.key, k.modified = strings.TrimSpace(string(b)), info.ModTime()
		slog.Info("read the ingest key", "file", k.path)
	}
	return k.key
}

// GetRequestMetadata sends the key with each gRPC export.
func (k *keySource) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + k.get()}, nil
}

// RequireTransportSecurity allows plain gRPC, as inside the Compose network.
func (k *keySource) RequireTransportSecurity() bool { return false }

// keyTransport sends the key with each OTLP/HTTP export.
type keyTransport struct {
	key  *keySource
	next http.RoundTripper
}

func (t keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.key.get())
	return t.next.RoundTrip(r)
}
