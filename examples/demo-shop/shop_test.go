// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func request(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, r))
	return w
}

func TestInventoryReservesAndRestocks(t *testing.T) {
	h := inventoryRoutes(newInventory()).mux
	if w := request(t, h, http.MethodGet, "/products", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "TEA-01") {
		t.Fatalf("products: %d %s", w.Code, w.Body.String())
	}
	w := request(t, h, http.MethodPost, "/reservations", `{"sku":"POT-01","quantity":5}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"priceCents":6400`) {
		t.Fatalf("reserve: %d %s", w.Code, w.Body.String())
	}
	// Out of stock is refused once, and restocked for the next shopper.
	if w := request(t, h, http.MethodPost, "/reservations", `{"sku":"POT-01","quantity":1}`); w.Code != http.StatusConflict {
		t.Errorf("out of stock: %d", w.Code)
	}
	if w := request(t, h, http.MethodPost, "/reservations", `{"sku":"POT-01","quantity":1}`); w.Code != http.StatusCreated {
		t.Errorf("after restocking: %d", w.Code)
	}
	for _, body := range []string{`{"sku":"NOPE","quantity":1}`, `{"sku":"TEA-01","quantity":0}`, `{"sku":"TEA-01","extra":1}`} {
		if w := request(t, h, http.MethodPost, "/reservations", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := request(t, h, http.MethodGet, "/health", ""); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/health+json" {
		t.Errorf("health: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestPaymentsChaos(t *testing.T) {
	psp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/charges" {
			writeJSON(w, http.StatusCreated, map[string]string{"id": "ch_1", "status": "succeeded"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer psp.Close()
	p := &payments{psp: psp.URL, chaos: chaosOff}
	h := p.routes().mux

	if w := request(t, h, http.MethodPost, "/payments", `{"orderId":"ord_1","amountCents":100}`); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "ch_1") {
		t.Fatalf("pay: %d %s", w.Code, w.Body.String())
	}
	health := func() (int, string) {
		w := request(t, h, http.MethodGet, "/health", "")
		var out struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Status
	}
	if code, status := health(); code != http.StatusOK || status != "pass" {
		t.Errorf("healthy: %d %s", code, status)
	}
	if w := request(t, h, http.MethodPut, "/chaos", `{"mode":"unhealthy"}`); w.Code != http.StatusOK {
		t.Fatalf("chaos: %d", w.Code)
	}
	if code, status := health(); code != http.StatusServiceUnavailable || status != "fail" {
		t.Errorf("unhealthy: %d %s", code, status)
	}
	// Payments still work while the health check fails.
	if w := request(t, h, http.MethodPost, "/payments", `{"orderId":"ord_2","amountCents":100}`); w.Code != http.StatusCreated {
		t.Errorf("pay while unhealthy: %d", w.Code)
	}
	request(t, h, http.MethodPut, "/chaos", `{"mode":"failing"}`)
	if code, status := health(); code != http.StatusOK || status != "warn" {
		t.Errorf("failing: %d %s", code, status)
	}
	failures := 0
	for range 40 {
		if w := request(t, h, http.MethodPost, "/payments", `{"orderId":"ord_3","amountCents":100}`); w.Code == http.StatusBadGateway {
			failures++
		}
	}
	if failures == 0 || failures == 40 {
		t.Errorf("failing mode failed %d of 40 payments", failures)
	}
	if w := request(t, h, http.MethodPut, "/chaos", `{"mode":"explode"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown mode: %d", w.Code)
	}
	psp.Close()
	request(t, h, http.MethodPut, "/chaos", `{"mode":"off"}`)
	if code, status := health(); code != http.StatusServiceUnavailable || status != "fail" {
		t.Errorf("provider down: %d %s", code, status)
	}
}

func TestStatement(t *testing.T) {
	for sql, want := range map[string][2]string{
		`INSERT INTO orders (id) VALUES ($1)`:            {"INSERT", "orders"},
		`SELECT id FROM orders WHERE id = $1`:            {"SELECT", "orders"},
		"CREATE TABLE IF NOT EXISTS orders (\n id text)": {"CREATE", "orders"},
		`select 1`: {"SELECT", ""},
		``:         {"", ""},
	} {
		if op, table := statement(sql); op != want[0] || table != want[1] {
			t.Errorf("%q = %s %s, want %v", sql, op, table, want)
		}
	}
}

func TestStorefrontForwards(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products":
			writeJSON(w, http.StatusOK, map[string]any{"data": []product{{SKU: "TEA-01"}}})
		case "/orders":
			body, _ := io.ReadAll(r.Body)
			writeJSON(w, http.StatusCreated, map[string]string{"got": string(body)})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer backend.Close()
	h := storefrontRoutes(backend.URL, backend.URL).mux
	if w := request(t, h, http.MethodGet, "/api/products", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "TEA-01") {
		t.Errorf("products: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, h, http.MethodPost, "/api/checkout", `{"items":[]}`); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `items`) {
		t.Errorf("checkout: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, h, http.MethodGet, "/api/orders/x", ""); w.Code != http.StatusBadGateway {
		t.Errorf("a failing backend: %d", w.Code)
	}
}

// fakeSignalbox answers the API calls the seed makes, and checks the
// session's Origin and CSRF token, as Signalbox does.
type fakeSignalbox struct {
	mu        sync.Mutex
	setUp     bool
	created   []string
	revoked   []string
	published int
}

func (f *fakeSignalbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet && r.Header.Get("Origin") != "http://localhost:8080" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	signedIn := r.URL.Path == "/readyz" || strings.HasPrefix(r.URL.Path, "/api/v1/setup") || r.URL.Path == "/api/v1/sessions"
	if !signedIn {
		if c, err := r.Cookie("session"); err != nil || c.Value != "s" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-CSRF-Token") != "csrf" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	key := r.Method + " " + r.URL.Path
	switch {
	case key == "GET /readyz":
		w.WriteHeader(http.StatusOK)
	case key == "GET /api/v1/setup":
		writeJSON(w, http.StatusOK, map[string]bool{"required": !f.setUp})
	case key == "POST /api/v1/setup":
		f.setUp = true
		writeJSON(w, http.StatusCreated, map[string]any{})
	case key == "POST /api/v1/sessions":
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "s", Path: "/"})
		writeJSON(w, http.StatusCreated, map[string]string{"csrfToken": "csrf"})
	case key == "GET /api/v1/workspaces":
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
	case key == "POST /api/v1/workspaces":
		f.created = append(f.created, "workspace")
		writeJSON(w, http.StatusCreated, map[string]string{"id": "ws_1"})
	case key == "GET /api/v1/workspaces/ws_1/projects":
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
	case key == "POST /api/v1/workspaces/ws_1/projects":
		f.created = append(f.created, "project")
		writeJSON(w, http.StatusCreated, map[string]string{"id": "prj_1"})
	case key == "GET /api/v1/projects/prj_1/services":
		// The storefront exists already, with its spec.
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "svc_storefront-web", "slug": "storefront-web"}}})
	case key == "POST /api/v1/projects/prj_1/services":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.created = append(f.created, fmtString(in["slug"]))
		writeJSON(w, http.StatusCreated, map[string]string{"id": "svc_" + fmtString(in["slug"])})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/specs"):
		if r.URL.Path == "/api/v1/services/svc_storefront-web/specs" {
			writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "spec_s"}}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/specs"):
		var in struct{ Content string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !strings.HasPrefix(in.Content, "openapi: 3.1.1") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": "spec_1"})
	case key == "POST /api/v1/specs/spec_1/versions":
		f.published++
		writeJSON(w, http.StatusCreated, map[string]any{})
	case key == "GET /api/v1/projects/prj_1/environments":
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "env_dev", "slug": "development"}, {"id": "env_prod", "slug": "production"}}})
	case key == "GET /api/v1/environments/env_prod/ingest-keys":
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "ing_old", "name": "demo-shop"}, {"id": "ing_other", "name": "ci"}}})
	case key == "DELETE /api/v1/ingest-keys/ing_old":
		f.revoked = append(f.revoked, "ing_old")
		w.WriteHeader(http.StatusNoContent)
	case key == "POST /api/v1/environments/env_prod/ingest-keys":
		writeJSON(w, http.StatusCreated, map[string]string{"key": "sbx_ing_new"})
	default:
		http.Error(w, "unexpected "+key, http.StatusNotFound)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func TestSeed(t *testing.T) {
	fake := &fakeSignalbox{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	keyFile := filepath.Join(t.TempDir(), "demo", "ingest-key")
	t.Setenv("SIGNALBOX_URL", srv.URL)
	t.Setenv("SIGNALBOX_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("SIGNALBOX_SETUP_CODE", "CODE")
	t.Setenv("SIGNALBOX_INGEST_KEY_FILE", keyFile)

	if err := runSeed(t.Context()); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(key)) != "sbx_ing_new" {
		t.Errorf("key file = %q, %v", key, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	// It completed setup, made what was missing, published three new specs,
	// and revoked only its own earlier key.
	if !fake.setUp || strings.Join(fake.created, " ") != "workspace project orders-api inventory-api payments-api" || fake.published != 3 ||
		strings.Join(fake.revoked, " ") != "ing_old" {
		t.Errorf("setUp %v, created %v, published %d, revoked %v", fake.setUp, fake.created, fake.published, fake.revoked)
	}
}

func TestKeyFollowsItsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingest-key")
	k := &keySource{path: path}
	if got := k.get(); got != "" {
		t.Errorf("before the seed wrote it: %q", got)
	}
	if err := os.WriteFile(path, []byte("sbx_ing_first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := k.get(); got != "sbx_ing_first" {
		t.Errorf("first key = %q", got)
	}
	// The seed replaces it on the next run.
	if err := os.WriteFile(path, []byte("sbx_ing_second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	md, _ := k.GetRequestMetadata(t.Context())
	if md["authorization"] != "Bearer sbx_ing_second" {
		t.Errorf("gRPC metadata = %v", md)
	}
	var sent string
	rt := keyTransport{key: k, next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent = r.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	if _, err := rt.RoundTrip(httptest.NewRequest(http.MethodPost, "http://signalbox:4318/v1/traces", nil)); err != nil || sent != "Bearer sbx_ing_second" {
		t.Errorf("HTTP header = %q, %v", sent, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
