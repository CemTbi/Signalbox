// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse/clickhousetest"
)

func monitorsHarness(t *testing.T) (*harness, httpapi.ProjectJSON, string) {
	t.Helper()
	ch, chURL := clickhousetest.NewWithURL(t)
	if err := clickhouse.Migrate(context.Background(), chURL, nil, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithTraces(t, ch)
	h.setUp()
	p := h.newProject("shop")
	var envs struct {
		Data []httpapi.EnvironmentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/environments"}).decode(t, &envs)
	return h, p, envs.Data[0].ID
}

func TestCollectionMonitors(t *testing.T) {
	h, p, env := monitorsHarness(t)
	failing := false
	shop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing && r.URL.Path == "/orders" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(shop.Close)
	var col httpapi.CollectionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/collections", body: map[string]string{"name": "Smoke"}}).decode(t, &col)
	h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + col.ID + "/items", body: map[string]any{
		"kind": "request", "name": "Orders", "request": map[string]any{"method": "GET", "url": shop.URL + "/orders"},
		"scripts": map[string]string{"test": `pm.test("ok", () => pm.response.to.have.status(200));`},
	}}).expect(t, http.StatusCreated, "")

	monitorsPath := "/api/v1/projects/" + p.ID + "/monitors"
	var m httpapi.MonitorJSON
	h.do(call{method: http.MethodPost, path: monitorsPath, body: map[string]any{
		"name": "Smoke", "kind": "collection", "environmentId": env, "collectionId": col.ID,
	}}).decode(t, &m)
	if m.Kind != "collection" || m.CollectionID != col.ID || m.Request != nil || m.TimeoutSeconds != 60 || m.CertificateWarningDays != 0 {
		t.Fatalf("created = %+v", m)
	}
	// A request belongs to HTTP monitors.
	h.do(call{method: http.MethodPost, path: monitorsPath, body: map[string]any{
		"name": "Smoke", "kind": "collection", "environmentId": env, "collectionId": col.ID, "request": map[string]any{"method": "GET", "url": shop.URL},
	}}).expect(t, http.StatusBadRequest, "validation-failed")

	var check httpapi.CheckJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/monitors/" + m.ID + "/check"}).decode(t, &check)
	if check.Outcome != "passed" || check.RunID == "" {
		t.Fatalf("check = %+v", check)
	}
	failing = true
	h.do(call{method: http.MethodPost, path: "/api/v1/monitors/" + m.ID + "/check"}).decode(t, &check)
	if check.Outcome != "failed" || check.FailureCode != "tests" || check.Status != 500 || !strings.Contains(check.FailureMessage, `first "ok" in Orders`) {
		t.Fatalf("failing check = %+v", check)
	}
	// The run is there to read, and says whose it is.
	var run httpapi.RunJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + check.RunID}).decode(t, &run)
	if run.MonitorID != m.ID || run.Status != "failed" || run.StartedBy != "" {
		t.Errorf("run = %+v", run)
	}
	// It isn't among the collection's runs.
	var runs struct {
		Data []httpapi.RunJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/collections/" + col.ID + "/runs"}).decode(t, &runs)
	if len(runs.Data) != 0 {
		t.Errorf("the collection's runs = %+v", runs.Data)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/monitors/" + m.ID}).decode(t, &m)
	if m.LastCheck == nil || m.LastCheck.RunID != check.RunID {
		t.Errorf("last check = %+v", m.LastCheck)
	}
	h.do(call{method: http.MethodPatch, path: "/api/v1/monitors/" + m.ID, contentType: "application/merge-patch+json", body: map[string]any{"kind": "http"}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPost, path: "/api/v1/monitors/" + m.ID + "/heartbeat-token"}).expect(t, http.StatusConflict, "not-a-heartbeat")
}

func TestHeartbeatMonitors(t *testing.T) {
	h, p, env := monitorsHarness(t)
	var m httpapi.MonitorJSON
	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/monitors", body: map[string]any{
		"name": "Nightly backup", "kind": "heartbeat", "environmentId": env, "intervalSeconds": 3600, "graceSeconds": 300,
	}})
	r.expect(t, http.StatusCreated, "")
	r.decode(t, &m)
	if m.Kind != "heartbeat" || m.GraceSeconds == nil || *m.GraceSeconds != 300 || m.TimeoutSeconds != 0 || m.FailuresToConfirm != 1 ||
		!strings.HasPrefix(m.HeartbeatToken, "sbx_hb_") || m.Request != nil {
		t.Fatalf("created = %+v", m)
	}
	monitorPath, token := "/api/v1/monitors/"+m.ID, m.HeartbeatToken
	m = httpapi.MonitorJSON{}
	h.do(call{method: http.MethodGet, path: monitorPath}).decode(t, &m)
	if m.HeartbeatToken != "" {
		t.Fatal("a heartbeat's token reads back")
	}

	// A job calls it with curl, signed in as no one.
	resp, err := h.srv.Client().Post(h.srv.URL+"/api/v1/heartbeats/"+token, "text/plain", strings.NewReader("done"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST = %d", resp.StatusCode)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/heartbeats/" + token}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: monitorPath}).decode(t, &m)
	if m.State != "up" || m.LastPingAt == "" {
		t.Fatalf("after a call = %+v", m)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/heartbeats/sbx_hb_nope"}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodPost, path: monitorPath + "/check"}).expect(t, http.StatusConflict, "not-checkable")

	// Rotating stops the old token at once.
	var rotated struct {
		HeartbeatToken string `json:"heartbeatToken"`
	}
	h.do(call{method: http.MethodPost, path: monitorPath + "/heartbeat-token"}).decode(t, &rotated)
	if rotated.HeartbeatToken == token || !strings.HasPrefix(rotated.HeartbeatToken, "sbx_hb_") {
		t.Fatalf("rotated = %q", rotated.HeartbeatToken)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/heartbeats/" + token}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodPost, path: "/api/v1/heartbeats/" + rotated.HeartbeatToken}).expect(t, http.StatusNoContent, "")

	// Calls are limited per token: 10 at once, then 10 a minute.
	limited := 0
	for range 12 {
		if h.do(call{method: http.MethodPost, path: "/api/v1/heartbeats/" + rotated.HeartbeatToken}).status == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("a token's calls aren't limited")
	}
	// Heartbeats have no request, and no timeout.
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/monitors", body: map[string]any{
		"name": "x", "kind": "heartbeat", "environmentId": env, "timeoutSeconds": 10,
	}}).expect(t, http.StatusBadRequest, "validation-failed")
}
