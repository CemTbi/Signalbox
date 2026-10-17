// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/app"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
)

func testConfig(databaseURL string) app.Config {
	cfg := app.DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.OpsAddr = "127.0.0.1:0"
	cfg.ShutdownTimeout = 5 * time.Second
	cfg.DatabaseURL = databaseURL
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
	srv, err := app.New(context.Background(), testConfig(url), slog.New(slog.DiscardHandler))
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

	resp, err := http.Get("http://" + srv.HTTPAddr() + "/api/v1/setup")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"required":true`) {
		t.Errorf("a new installation should need setup: %s", b)
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
		{"unavailable role", func(c *app.Config) { c.Roles = "ingest" }, "not available"},
		{"no database", func(c *app.Config) { c.DatabaseURL = "" }, "SIGNALBOX_DATABASE_URL"},
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
