// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/CemTbi/Signalbox/internal/app"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func testConfig(databaseURL string) app.Config {
	cfg := app.DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.OpsAddr = "127.0.0.1:0"
	cfg.OTLPGRPCAddr = "127.0.0.1:0"
	cfg.OTLPHTTPAddr = "127.0.0.1:0"
	cfg.ZipkinAddr = "127.0.0.1:0"
	cfg.ShutdownTimeout = 5 * time.Second
	cfg.DatabaseURL = databaseURL
	// Nothing listens here: tests that start the server set a real one.
	cfg.ClickHouseURL = "clickhouse://u:p@127.0.0.1:1/db"
	cfg.DatabaseWait = 10 * time.Second
	cfg.SetupCode = "TEST-SETU-PCOD-E123"
	cfg.Identity.Argon2 = identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	return cfg
}

func readyStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct{ Status, Output string }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body.Status
}

func TestServerStartsServesAndStops(t *testing.T) {
	_, url := postgrestest.NewWithURL(t)
	ch, chURL := clickhousetest.NewWithURL(t)
	cfg := testConfig(url)
	cfg.ClickHouseURL = chURL
	cfg.TraceRetentionDays = 3
	srv, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// The api listener answers once startup is complete.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if code, _ := readyStatus(t, "http://"+srv.OpsAddr()+"/readyz"); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code, status := readyStatus(t, "http://"+srv.HTTPAddr()+"/readyz"); code != http.StatusOK || status != "pass" {
		t.Errorf("api /readyz = %d %q, want 200 pass", code, status)
	}
	if ready := get(t, "http://"+srv.OpsAddr()+"/readyz"); !strings.Contains(ready, "clickhouse:responseTime") {
		t.Errorf("readiness doesn't check ClickHouse: %s", ready)
	}
	// Startup migrated ClickHouse and set the retention.
	var engine string
	if err := ch.QueryRow(context.Background(), `SELECT engine_full FROM system.tables WHERE database = currentDatabase() AND name = 'spans'`).Scan(&engine); err != nil {
		t.Fatalf("no spans table: %v", err)
	}
	if !strings.Contains(engine, "toIntervalDay(3)") {
		t.Errorf("spans = %s, want a 3-day TTL", engine)
	}

	resp, err := http.Get("http://" + srv.HTTPAddr() + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"required":true`) {
		t.Errorf("a new installation should need setup: %s", b)
	}

	// The ops listener serves metrics, which count the request above by
	// its route; the api listener doesn't serve them.
	metrics := get(t, "http://"+srv.OpsAddr()+"/metrics")
	for _, want := range []string{
		`signalbox_http_requests_total{route="/api/v1/setup",method="GET",code="200"} 1`,
		`signalbox_build_info{version="`,
		"# TYPE signalbox_runner_requests_total counter",
		"# TYPE signalbox_scripts_total counter",
		"signalbox_agents_online 0",
		"signalbox_test_runs_running 0",
		"signalbox_db_connections_max ",
		"go_goroutines ",
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("/metrics lacks %q:\n%s", want, metrics)
		}
	}
	if strings.Contains(get(t, "http://"+srv.HTTPAddr()+"/metrics"), "go_goroutines") {
		t.Error("the api listener serves metrics")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunFailsWhenTheDatabaseRejectsCredentials(t *testing.T) {
	_, url := postgrestest.NewWithURL(t)
	bad := strings.Replace(url, ":postgres@", ":wrong@", 1)
	if bad == url {
		t.Skip("the test database URL has no password to replace")
	}
	srv, err := app.New(context.Background(), testConfig(bad), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "password") {
			t.Errorf("Run = %v, want a password error", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run kept retrying a wrong password")
	}
}

func TestStopDuringStartupIsClean(t *testing.T) {
	// Nothing listens on this port, so startup waits for the database.
	srv, err := app.New(context.Background(), testConfig("postgres://u:p@127.0.0.1:1/db?connect_timeout=1"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	code, _ := readyStatus(t, "http://"+srv.OpsAddr()+"/readyz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("readiness while waiting for the database = %d, want 503", code)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v after a stop during startup, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestNewValidatesConfiguration(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tests := []struct {
		name    string
		change  func(*app.Config)
		wantErr string
	}{
		{"busy port", func(c *app.Config) { c.HTTPAddr = ln.Addr().String() }, "listen on"},
		{"unavailable role", func(c *app.Config) { c.Roles = "scheduler" }, "not available"},
		{"no ingest rate", func(c *app.Config) { c.IngestSpansPerSecond = 0 }, "ingest rate"},
		{"no database", func(c *app.Config) { c.DatabaseURL = "" }, "SIGNALBOX_DATABASE_URL"},
		{"no ClickHouse", func(c *app.Config) { c.ClickHouseURL = "" }, "SIGNALBOX_CLICKHOUSE_URL"},
		{"bad ClickHouse URL", func(c *app.Config) { c.ClickHouseURL = "clickhouse://u:p@[::1/db" }, "ClickHouse URL"},
		{"no retention", func(c *app.Config) { c.TraceRetentionDays = 0 }, "retention"},
		{"too long a retention", func(c *app.Config) { c.TraceRetentionDays = 3651 }, "retention"},
		{"public URL with a path", func(c *app.Config) { c.PublicURL = "https://example.com/signalbox" }, "root of its host"},
		{"bad trusted proxy", func(c *app.Config) { c.TrustedProxies = "10.0.0.0/33" }, "trusted proxies"},
		{"weak setup code", func(c *app.Config) { c.SetupCode = "abc" }, "setup code"},
		{"single sign-on without a client ID", func(c *app.Config) { c.OIDC.Issuer = "https://idp.example.com" }, "SIGNALBOX_OIDC_CLIENT_ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig("postgres://u:p@127.0.0.1:1/db")
			tt.change(&cfg)
			_, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParsePublicURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://signalbox.example.com":   "https://signalbox.example.com",
		"https://signalbox.example.com/":  "https://signalbox.example.com",
		" http://localhost:8080 ":         "http://localhost:8080",
		"http://192.0.2.10:8080":          "http://192.0.2.10:8080",
		"ftp://example.com":               "",
		"signalbox.example.com":           "",
		"https://user:pw@example.com":     "",
		"https://example.com/?next=/home": "",
	} {
		u, err := app.ParsePublicURL(in)
		switch {
		case want == "" && err == nil:
			t.Errorf("ParsePublicURL(%q) = %s, want an error", in, u)
		case want != "" && (err != nil || u.String() != want):
			t.Errorf("ParsePublicURL(%q) = %v, %v, want %s", in, u, err, want)
		}
	}
}

func TestIngestStoresSpans(t *testing.T) {
	pg, url := postgrestest.NewWithURL(t)
	ch, chURL := clickhousetest.NewWithURL(t)
	cfg := testConfig(url)
	cfg.ClickHouseURL = chURL
	cfg.Roles = "ingest" // the ingest role runs on its own
	srv, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if srv.HTTPAddr() != "" {
		t.Error("the ingest role alone serves the API")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if code, _ := readyStatus(t, "http://"+srv.OpsAddr()+"/readyz"); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A key for a project's first environment.
	log := slog.New(slog.DiscardHandler)
	ws := workspaces.New(pg, log)
	owner := uuid.Must(uuid.NewV7())
	if _, err := pg.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, 'ada@example.com', 'Ada')`, owner); err != nil {
		t.Fatal(err)
	}
	w, err := ws.CreateWorkspace(ctx, owner, workspaces.WorkspaceInput{Name: "Shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProject(ctx, owner, w.ID, workspaces.ProjectInput{Name: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	envs, err := ws.ListEnvironments(ctx, owner, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := telemetry.NewKeys(pg, log, ws).Create(ctx, owner, envs[0].ID, "test")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now().Add(-time.Second)
	send := func(url, contentType, body string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+key.Key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("%s: %d %s", url, resp.StatusCode, b)
		}
	}
	send("http://"+srv.OTLPHTTPAddr()+"/v1/traces", "application/json", fmt.Sprintf(
		`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"orders"}}]},"scopeSpans":[{"spans":[
		{"traceId":"%s","spanId":"0102030405060708","name":"GET /orders","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d"}]}]}]}`,
		strings.Repeat("ab", 16), start.UnixNano(), start.Add(time.Millisecond).UnixNano()))
	send("http://"+srv.ZipkinAddr()+"/api/v2/spans", "application/json", fmt.Sprintf(
		`[{"traceId":"%s","id":"1112131415161718","name":"get","kind":"SERVER","timestamp":%d,"duration":900,"localEndpoint":{"serviceName":"web"}}]`,
		strings.Repeat("cd", 16), start.UnixMicro()))

	conn, err := grpc.NewClient(srv.OTLPGRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = coltracepb.NewTraceServiceClient(conn).Export(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+key.Key),
		&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "payments"}}}}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId: bytes.Repeat([]byte{0xef}, 16), SpanId: bytes.Repeat([]byte{0x01}, 8), Name: "charge", Kind: tracepb.Span_SPAN_KIND_SERVER,
				StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(time.Millisecond).UnixNano()),
			}}}},
		}}})
	if err != nil {
		t.Fatalf("OTLP/gRPC: %v", err)
	}

	// All three were stored before their requests were answered.
	var services []string
	rows, err := ch.Query(ctx, `SELECT service_name FROM spans WHERE environment_id = {env:UUID} ORDER BY service_name`, clickhouse.Named("env", envs[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		services = append(services, s)
	}
	if strings.Join(services, " ") != "orders payments web" {
		t.Errorf("stored spans of %v", services)
	}
}
