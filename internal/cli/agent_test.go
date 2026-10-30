// SPDX-License-Identifier: Apache-2.0

package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/cli"
)

// fakeAgentServer plays a Signalbox server for an agent: it hands out the
// jobs it is given, one per request for work, and collects the results.
type fakeAgentServer struct {
	*httptest.Server
	jobs     chan map[string]any
	results  chan map[string]any
	mu       sync.Mutex
	connects []map[string]any
	// deleteOnce makes the first request for work answer 404, as for an
	// agent someone deleted.
	deleteOnce bool
}

func newFakeAgentServer(t *testing.T) *fakeAgentServer {
	t.Helper()
	f := &fakeAgentServer{jobs: make(chan map[string]any, 4), results: make(chan map[string]any, 4)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sbx_pat_good" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"status":401,"detail":"Sign in, or send a valid token."}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			f.connects = append(f.connects, in)
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":"agt_1","name":"laptop"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/agt_1/jobs/next":
			if r.URL.Query().Get("waitMs") == "" {
				t.Error("the agent asked for work without waitMs")
			}
			f.mu.Lock()
			deleted := f.deleteOnce
			f.deleteOnce = false
			f.mu.Unlock()
			if deleted {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"status":404,"detail":"There is no agent or job with this ID."}`)
				return
			}
			select {
			case j := <-f.jobs:
				_ = json.NewEncoder(w).Encode(j)
			case <-time.After(50 * time.Millisecond):
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/agents/agt_1/jobs/"):
			var res map[string]any
			_ = json.NewDecoder(r.Body).Decode(&res)
			res["job"] = strings.TrimPrefix(r.URL.Path, "/api/v1/agents/agt_1/jobs/")
			f.results <- res
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestAgentSendsJobsFromThisMachine(t *testing.T) {
	// A service only this machine can reach.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "%s %s %s %s", r.Method, r.URL.Path, r.Header.Get("X-Token"), body)
	}))
	t.Cleanup(local.Close)
	api := newFakeAgentServer(t)
	api.deleteOnce = true
	api.jobs <- map[string]any{
		"id": "job_1", "method": "POST", "url": local.URL + "/orders?key=secret",
		"headers": []map[string]string{{"key": "X-Token", "value": "t0k3n"}},
		"body":    base64.StdEncoding.EncodeToString([]byte(`{"id":42}`)), "timeoutMs": 5000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := cli.NewRootCommand(strings.NewReader(""), &out, &errOut)
	cmd.SetArgs([]string{"agent", "--server", api.URL, "--token", "sbx_pat_good", "--name", "laptop"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	var res map[string]any
	select {
	case res = <-api.results:
	case <-ctx.Done():
		t.Fatalf("no result came back\n%s\n%s", out.String(), errOut.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent: %v\n%s", err, errOut.String())
	}

	if res["job"] != "job_1" {
		t.Errorf("result for %v", res["job"])
	}
	resp, _ := res["response"].(map[string]any)
	if resp == nil || resp["status"] != float64(200) {
		t.Fatalf("result = %v", res)
	}
	if body, _ := base64.StdEncoding.DecodeString(fmt.Sprint(resp["body"])); string(body) != `POST /orders t0k3n {"id":42}` {
		t.Errorf("the local service got %q", body)
	}
	// The deleted agent connected again, under the same name.
	api.mu.Lock()
	connects := api.connects
	api.mu.Unlock()
	if len(connects) != 2 || connects[1]["name"] != "laptop" || connects[0]["os"] == "" {
		t.Errorf("connects = %v", connects)
	}
	// What it printed leaves out the query, which may hold credentials.
	got := out.String()
	for _, want := range []string{"Agent laptop is connected", "POST " + local.URL + "/orders → 200", "Stopped."} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret") {
		t.Errorf("output shows the query:\n%s", got)
	}
	if !strings.Contains(errOut.String(), "deleted on the server") {
		t.Errorf("stderr = %s", errOut.String())
	}
}

func TestAgentRefusedToken(t *testing.T) {
	api := newFakeAgentServer(t)
	code, _, _, err := runErr(t, "agent", "--server", api.URL, "--token", "sbx_pat_bad")
	if code != cli.ExitUsage || err == nil || !strings.Contains(err.Error(), "valid token") {
		t.Errorf("exit %d: %v", code, err)
	}
}

func TestAgentUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "--token", "t"},
		{"agent", "--server", "https://signalbox.example.com"},
		{"agent", "--server", "ftp://signalbox.example.com", "--token", "t"},
		{"agent", "--server", "https://signalbox.example.com", "--token", "t", "--concurrency", "0"},
		{"agent", "--server", "https://signalbox.example.com", "--token", "t", "--deny", "10.0.0.0"},
	} {
		if code, _, stderr, _ := runErr(t, args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d: %s", args, code, stderr)
		}
	}
}
