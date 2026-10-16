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
)

func testConfig() app.Config {
	cfg := app.DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.OpsAddr = "127.0.0.1:0"
	cfg.ShutdownTimeout = 5 * time.Second
	return cfg
}

func TestServerStartsServesAndStops(t *testing.T) {
	srv, err := app.New(context.Background(), testConfig(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	for _, url := range []string{
		"http://" + srv.OpsAddr() + "/readyz",
		"http://" + srv.HTTPAddr() + "/readyz",
	} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		var body struct{ Status string }
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || body.Status != "pass" {
			t.Errorf("GET %s = %d %q, want 200 pass", url, resp.StatusCode, body.Status)
		}
	}

	resp, err := http.Get("http://" + srv.HTTPAddr() + "/api/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"roles":["api"]`) {
		t.Errorf("meta = %s", b)
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

func TestNewFailsOnBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg := testConfig()
	cfg.HTTPAddr = ln.Addr().String()
	if _, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler)); err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Errorf("New with a busy port = %v, want a listen error", err)
	}
}

func TestNewRejectsUnavailableRole(t *testing.T) {
	cfg := testConfig()
	cfg.Roles = "ingest"
	if _, err := app.New(context.Background(), cfg, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("New accepted a role this version can't run")
	}
}
