// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/httpapi"
	"github.com/CemTbi/Signalbox/internal/platform/id"
)

func TestSendingThroughAnAgent(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	r := h.do(call{method: http.MethodPost, path: "/api/v1/tokens", body: map[string]any{"name": "Agent"}})
	var tok httpapi.NewAccessTokenJSON
	r.decode(t, &tok)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Token}

	// The agent connects with the token, and again after a restart.
	var ag httpapi.AgentJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/agents", headers: bearer, body: map[string]string{"name": "laptop", "os": "linux/amd64"}}).decode(t, &ag)
	var again httpapi.AgentJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/agents", headers: bearer, body: map[string]string{"name": "laptop", "version": "0.2.0"}}).decode(t, &again)
	if ag.ID == "" || again.ID != ag.ID || again.Version != "0.2.0" {
		t.Fatalf("agent = %+v, again %+v", ag, again)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/agents", headers: bearer, body: map[string]string{"os": "linux"}, invalid: true}).
		expect(t, http.StatusBadRequest, "validation-failed")
	var list struct {
		Data []httpapi.AgentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/agents"}).decode(t, &list)
	if len(list.Data) != 1 || list.Data[0].ID != ag.ID || list.Data[0].Online {
		t.Errorf("agents = %+v", list)
	}

	// The agent takes the job and answers it, as if from its machine.
	jobs := "/api/v1/agents/" + ag.ID + "/jobs/"
	taken := make(chan httpapi.AgentJobJSON, 1)
	go func() {
		defer close(taken)
		for range 3 {
			r := h.do(call{method: http.MethodPost, path: jobs + "next?waitMs=5000", headers: bearer})
			if r.status == http.StatusNoContent {
				continue
			}
			if r.header.Get("Cache-Control") != "no-store" {
				t.Errorf("a job with Cache-Control %q", r.header.Get("Cache-Control"))
			}
			var j httpapi.AgentJobJSON
			r.decode(t, &j)
			body, _ := base64.StdEncoding.DecodeString(j.Body)
			h.do(call{method: http.MethodPut, path: jobs + j.ID, headers: bearer, body: map[string]any{
				"response": map[string]any{
					"status": 200, "statusText": "200 OK", "proto": "HTTP/1.1",
					"headers": []map[string]string{{"key": "Content-Type", "value": "text/plain"}},
					"body":    base64.StdEncoding.EncodeToString(append([]byte("from the laptop: "), body...)),
					"url":     j.URL, "remoteAddress": "127.0.0.1:3000",
				},
				"timings": map[string]float64{"dnsMs": 0, "connectMs": 0.2, "tlsMs": 0, "firstByteMs": 1.5, "downloadMs": 0.1, "totalMs": 1.8},
			}}).expect(t, http.StatusNoContent, "")
			taken <- j
			return
		}
		t.Error("the agent got no job")
	}()
	r = h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/send", body: map[string]any{
		"agentId": ag.ID, "method": "POST", "url": "http://localhost:3000/orders", "body": "hello",
		"headers": []map[string]string{{"key": "Authorization", "value": "Bearer secret"}},
	}})
	r.expect(t, http.StatusOK, "")
	var sent httpapi.HistoryEntryJSON
	r.decode(t, &sent)
	j := <-taken
	if j.Method != http.MethodPost || j.URL != "http://localhost:3000/orders" || len(j.Headers) == 0 || j.Headers[0].Value != "Bearer secret" {
		t.Errorf("job = %+v", j)
	}
	if sent.Agent != "laptop" || sent.Response == nil || sent.Response.Body != "from the laptop: hello" || sent.Response.Status != 200 {
		t.Fatalf("sent = %+v", sent)
	}
	var kept httpapi.HistoryEntryJSON
	h.do(call{method: http.MethodGet, path: "/api/v1/history/" + sent.ID}).decode(t, &kept)
	if kept.Agent != "laptop" || kept.Request.Headers[0].Value != "•••" {
		t.Errorf("history = %+v", kept)
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/agents"}).decode(t, &list)
	if len(list.Data) != 1 || !list.Data[0].Online {
		t.Errorf("after polling, agents = %+v", list)
	}

	// A result for a job that has one, or isn't the agent's, goes nowhere;
	// a failure must be one the runner knows.
	none := map[string]any{"failure": map[string]any{"code": "connect", "message": "Couldn't connect."}, "timings": map[string]float64{"dnsMs": 0, "connectMs": 0, "tlsMs": 0, "firstByteMs": 0, "downloadMs": 0, "totalMs": 0}}
	h.do(call{method: http.MethodPut, path: jobs + j.ID, headers: bearer, body: none}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodPut, path: jobs + id.Format(id.Job, id.New()), headers: bearer, invalid: true, body: map[string]any{
		"failure": map[string]any{"code": "agent-offline", "message": "Made up."}, "timings": none["timings"],
	}}).expect(t, http.StatusBadRequest, "validation-failed")
	h.do(call{method: http.MethodPost, path: jobs + "next?waitMs=30000", headers: bearer, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")

	// Someone else can't see, use or delete the agent.
	h.createUser("Eve", "eve@example.com")
	h.signIn("eve@example.com")
	h.do(call{method: http.MethodGet, path: "/api/v1/agents"}).decode(t, &list)
	if len(list.Data) != 0 {
		t.Errorf("eve sees %+v", list)
	}
	h.do(call{method: http.MethodDelete, path: "/api/v1/agents/" + ag.ID}).expect(t, http.StatusNotFound, "not-found")
	h.do(call{method: http.MethodPost, path: jobs + "next?waitMs=0"}).expect(t, http.StatusNotFound, "not-found")
	q := h.newProject("eves")
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + q.ID + "/send", body: map[string]any{"agentId": ag.ID, "method": "GET", "url": "http://localhost:3000/"}}).
		expect(t, http.StatusNotFound, "not-found")

	// Deleted, the agent's next request for work fails, so it connects again.
	h.signIn("ada@example.com")
	h.do(call{method: http.MethodDelete, path: "/api/v1/agents/" + ag.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodPost, path: jobs + "next?waitMs=0", headers: bearer}).expect(t, http.StatusNotFound, "not-found")
}

func TestRunningThroughAnAgent(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var ag httpapi.AgentJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/agents", body: map[string]string{"name": "laptop"}}).decode(t, &ag)
	var col httpapi.CollectionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/collections", body: map[string]string{"name": "Local API"}}).decode(t, &col)
	h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + col.ID + "/items", body: map[string]any{
		"kind": "request", "name": "Health", "request": map[string]any{"method": "GET", "url": "http://localhost:3000/health"},
		"scripts": map[string]string{"test": `pm.test("up", () => pm.expect(pm.response.text()).to.eql("up"));`},
	}}).expect(t, http.StatusCreated, "")

	// The agent answers every job until the run ends.
	stop := make(chan struct{})
	served := make(chan int, 1)
	go func() {
		n := 0
		defer func() { served <- n }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r := h.do(call{method: http.MethodPost, path: "/api/v1/agents/" + ag.ID + "/jobs/next?waitMs=200"})
			if r.status != http.StatusOK {
				continue
			}
			var j httpapi.AgentJobJSON
			r.decode(t, &j)
			h.do(call{method: http.MethodPut, path: "/api/v1/agents/" + ag.ID + "/jobs/" + j.ID, body: map[string]any{
				"response": map[string]any{"status": 200, "headers": []map[string]string{}, "body": base64.StdEncoding.EncodeToString([]byte("up"))},
				"timings":  map[string]float64{"dnsMs": 0, "connectMs": 0, "tlsMs": 0, "firstByteMs": 0, "downloadMs": 0, "totalMs": 0.5},
			}}).expect(t, http.StatusNoContent, "")
			n++
		}
	}()

	r := h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + col.ID + "/runs", body: map[string]any{"agentId": ag.ID, "iterations": 2}})
	r.expect(t, http.StatusAccepted, "")
	var run httpapi.RunJSON
	r.decode(t, &run)
	if run.AgentID != ag.ID || run.Agent != "laptop" {
		t.Errorf("started = %+v", run)
	}
	deadline := time.Now().Add(30 * time.Second)
	for run.Status == "queued" || run.Status == "running" {
		if time.Now().After(deadline) {
			t.Fatalf("run still %s", run.Status)
		}
		time.Sleep(50 * time.Millisecond)
		h.do(call{method: http.MethodGet, path: "/api/v1/runs/" + run.ID}).decode(t, &run)
	}
	close(stop)
	if n := <-served; n != 2 || run.Status != "passed" || run.Totals.Tests != 2 || run.Agent != "laptop" {
		t.Errorf("the agent served %d; run = %+v", n, run)
	}

	// Someone else's agent, or none, can't run it.
	h.do(call{method: http.MethodPost, path: "/api/v1/collections/" + col.ID + "/runs", body: map[string]any{"agentId": id.Format(id.Agent, id.New())}}).
		expect(t, http.StatusNotFound, "not-found")
}
