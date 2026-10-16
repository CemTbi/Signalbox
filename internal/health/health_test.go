// SPDX-License-Identifier: Apache-2.0

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/health"
)

func check(name string, critical bool, status health.Status) health.Check {
	return health.Check{
		Name: name, ComponentType: "component", Critical: critical,
		Run: func(context.Context) health.Result { return health.Result{Status: status} },
	}
}

func TestReadyCombinesChecks(t *testing.T) {
	tests := []struct {
		name   string
		checks []health.Check
		want   health.Status
		code   int
	}{
		{"no checks", nil, health.Pass, http.StatusOK},
		{"all pass", []health.Check{check("a", true, health.Pass), check("b", false, health.Pass)}, health.Pass, http.StatusOK},
		{"non-critical failure warns", []health.Check{check("a", true, health.Pass), check("b", false, health.Fail)}, health.Warn, http.StatusOK},
		{"critical failure fails", []health.Check{check("a", true, health.Fail), check("b", false, health.Pass)}, health.Fail, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := health.New("svc", "1.2.3")
			for _, ch := range tt.checks {
				c.Add(ch)
			}
			rec := httptest.NewRecorder()
			c.ReadyHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != tt.code {
				t.Errorf("status code %d, want %d", rec.Code, tt.code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != health.MediaType {
				t.Errorf("Content-Type %q, want %q", ct, health.MediaType)
			}
			var resp health.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Status != tt.want || resp.ReleaseID != "1.2.3" || resp.ServiceID != "svc" {
				t.Errorf("got %+v, want status %s", resp, tt.want)
			}
			if len(resp.Checks) != len(tt.checks) {
				t.Errorf("got %d checks in the response, want %d", len(resp.Checks), len(tt.checks))
			}
		})
	}
}

func TestSlowCheckTimesOutAsFailure(t *testing.T) {
	c := health.New("svc", "1")
	c.Add(health.Check{Name: "slow", Critical: true, Run: func(ctx context.Context) health.Result {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		return health.Result{Status: health.Pass}
	}})
	start := time.Now()
	resp := c.Ready(context.Background())
	if resp.Status != health.Fail {
		t.Errorf("status %s, want fail", resp.Status)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Ready waited %v for a hanging check", elapsed)
	}
}

func TestPanickingCheckFails(t *testing.T) {
	c := health.New("svc", "1")
	c.Add(health.Check{Name: "boom", Critical: true, Run: func(context.Context) health.Result { panic("oops") }})
	if resp := c.Ready(context.Background()); resp.Status != health.Fail {
		t.Errorf("status %s, want fail", resp.Status)
	}
}

func TestDrainingFailsReadinessButNotLiveness(t *testing.T) {
	c := health.New("svc", "1")
	c.SetDraining()
	if resp := c.Ready(context.Background()); resp.Status != health.Fail || resp.Output != "shutting down" {
		t.Errorf("Ready while draining = %+v", resp)
	}
	if resp := c.Live(); resp.Status != health.Pass {
		t.Errorf("Live while draining = %+v", resp)
	}
}
