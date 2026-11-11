// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheAccessLogKeepsNoHeartbeatToken(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	h := logRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/heartbeats/sbx_hb_secret-part", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/monitors/mon_1", nil))
	logged := out.String()
	if strings.Contains(logged, "secret-part") || !strings.Contains(logged, "path=/api/v1/heartbeats/{token}") {
		t.Errorf("logged %s", logged)
	}
	if !strings.Contains(logged, "path=/api/v1/monitors/mon_1") {
		t.Errorf("other paths aren't logged as they are: %s", logged)
	}
}
