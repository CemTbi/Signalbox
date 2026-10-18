// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestEnvironmentVariables(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the key, as some APIs do, to check history masks responses.
		_, _ = w.Write([]byte(`{"key":"` + r.Header.Get("X-Api-Key") + `"}`))
	}))
	defer target.Close()
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")

	var envs struct {
		Data []httpapi.EnvironmentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/environments"}).decode(t, &envs)
	if len(envs.Data) != 3 || envs.Data[0].Slug != "development" || envs.Data[2].Kind != "production" {
		t.Fatalf("environments = %+v", envs)
	}
	staging := envs.Data[1]
	r := h.do(call{method: http.MethodGet, path: "/api/v1/environments/" + staging.ID})
	r.expect(t, http.StatusOK, "")
	var got httpapi.EnvironmentJSON
	r.decode(t, &got)
	if got != staging || r.header.Get("ETag") == "" {
		t.Errorf("environment = %+v, ETag %q", got, r.header.Get("ETag"))
	}

	vars := "/api/v1/environments/" + staging.ID + "/variables"
	r = h.do(call{method: http.MethodGet, path: vars})
	r.expect(t, http.StatusOK, "")
	if r.header.Get("ETag") != `"0"` {
		t.Errorf("ETag before = %q", r.header.Get("ETag"))
	}
	r = h.do(call{method: http.MethodPut, path: vars, headers: map[string]string{"If-Match": `"0"`}, body: map[string]any{"variables": []map[string]any{
		{"key": "baseUrl", "value": target.URL},
		{"key": "apiKey", "value": "sk_live_8f2b", "secret": true},
	}}})
	r.expect(t, http.StatusOK, "")
	var stored httpapi.VariablesJSON
	r.decode(t, &stored)
	if len(stored.Variables) != 2 || stored.Variables[1].Value != nil || !stored.Variables[1].Secret || r.header.Get("ETag") != `"1"` ||
		strings.Contains(string(r.body), "sk_live") {
		t.Errorf("stored = %s, ETag %q", r.body, r.header.Get("ETag"))
	}
	h.do(call{method: http.MethodPut, path: vars, headers: map[string]string{"If-Match": `"0"`}, body: map[string]any{"variables": []any{}}}).
		expect(t, http.StatusPreconditionFailed, "edit-conflict")
	h.do(call{method: http.MethodPut, path: vars, invalid: true, body: map[string]any{"variables": []map[string]any{{"key": "has space"}}}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPut, path: vars, invalid: true, body: map[string]any{}}).
		expect(t, http.StatusBadRequest, "validation-failed")

	send := "/api/v1/projects/" + p.ID + "/send"
	r = h.do(call{method: http.MethodPost, path: send, body: map[string]any{
		"environmentId": staging.ID, "method": "GET", "url": "{{baseUrl}}/orders/{{orderId}}",
		"headers": []map[string]string{{"key": "X-Api-Key", "value": "{{apiKey}}"}},
	}})
	r.expect(t, http.StatusOK, "")
	var sent httpapi.HistoryEntryJSON
	r.decode(t, &sent)
	// What comes back is whole: it is the sender's own.
	if sent.Response == nil || sent.Response.Body != `{"key":"sk_live_8f2b"}` || sent.EnvironmentID != staging.ID ||
		len(sent.Unresolved) != 1 || sent.Unresolved[0] != "orderId" || sent.URL != target.URL+"/orders/{{orderId}}" {
		t.Fatalf("sent = %+v", sent)
	}
	r = h.do(call{method: http.MethodGet, path: "/api/v1/history/" + sent.ID})
	if strings.Contains(string(r.body), "sk_live") {
		t.Errorf("history holds the secret: %s", r.body)
	}

	h.do(call{method: http.MethodPost, path: send, invalid: true, body: map[string]any{"environmentId": "prj_nope", "method": "GET", "url": target.URL}}).
		expect(t, http.StatusBadRequest, "validation-failed")
	other := h.newProject("billing")
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + other.ID + "/send", body: map[string]any{"environmentId": staging.ID, "method": "GET", "url": target.URL}}).
		expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/env_01j9z3n5q7s9v1w3y5a7c9e1g3/variables"}).expect(t, http.StatusNotFound, "not-found")
}
