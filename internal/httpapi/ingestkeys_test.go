// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestIngestKeys(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var envs struct {
		Data []httpapi.EnvironmentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/environments"}).decode(t, &envs)
	keys := "/api/v1/environments/" + envs.Data[2].ID + "/ingest-keys"

	r := h.do(call{method: http.MethodPost, path: keys, body: map[string]string{"name": "checkout-service"}})
	r.expect(t, http.StatusCreated, "")
	if r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("a new key with Cache-Control %q", r.header.Get("Cache-Control"))
	}
	var k httpapi.NewIngestKeyJSON
	r.decode(t, &k)
	if !strings.HasPrefix(k.Key, "sbx_ing_") || !strings.HasPrefix(k.ID, "ing_") || k.Name != "checkout-service" ||
		k.EnvironmentID != envs.Data[2].ID || k.CreatedBy == nil || k.LastUsedAt != nil || r.header.Get("Location") != "/api/v1/ingest-keys/"+k.ID {
		t.Errorf("key = %+v, Location %s", k, r.header.Get("Location"))
	}
	h.do(call{method: http.MethodPost, path: keys, body: map[string]string{}, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	// The list never shows a secret.
	r = h.do(call{method: http.MethodGet, path: keys})
	var list struct {
		Data []httpapi.IngestKeyJSON `json:"data"`
	}
	r.decode(t, &list)
	if len(list.Data) != 1 || list.Data[0].ID != k.ID || list.Data[0].Hint != k.Hint || strings.Contains(string(r.body), k.Key) {
		t.Errorf("list = %s", r.body)
	}

	h.do(call{method: http.MethodDelete, path: "/api/v1/ingest-keys/" + k.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodDelete, path: "/api/v1/ingest-keys/" + k.ID}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodGet, path: "/api/v1/environments/env_01h455vb4pex5vsknk084sn02q/ingest-keys"}).expect(t, http.StatusNotFound, "not-found")

	// Someone signed out can't see them.
	h.signOutLocally()
	h.do(call{method: http.MethodGet, path: keys}).expect(t, http.StatusUnauthorized, "")
}
