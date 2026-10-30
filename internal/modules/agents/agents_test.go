// SPDX-License-Identifier: Apache-2.0

package agents_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/agents"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/postgres/postgrestest"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func setup(t *testing.T) (context.Context, *agents.Module, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := postgrestest.New(t)
	ctx := context.Background()
	users := make([]uuid.UUID, 2)
	for i, name := range []string{"ada", "eve"} {
		users[i] = id.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, users[i], name+"@example.com", name); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, agents.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), users[0], users[1]
}

func TestConnectAndList(t *testing.T) {
	ctx, mod, ada, eve := setup(t)
	a, err := mod.Connect(ctx, ada, agents.Input{Name: " laptop ", Hostname: "ada-mbp", OS: "darwin/arm64", Version: "0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	// Connecting again with the name is the same agent.
	again, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop", Hostname: "ada-mbp", OS: "darwin/arm64", Version: "0.2.1"})
	if err != nil || again.ID != a.ID || again.Version != "0.2.1" {
		t.Errorf("again = %+v, %v", again, err)
	}
	if _, err := mod.Connect(ctx, ada, agents.Input{Name: " "}); !errors.As(err, new(validate.Errors)) {
		t.Errorf("no name: %v", err)
	}
	list, err := mod.List(ctx, ada)
	if err != nil || len(list) != 1 || list[0].Online {
		t.Errorf("list = %+v, %v", list, err)
	}
	// Asking for work makes it online.
	if j, err := mod.Next(ctx, ada, a.ID, 0); err != nil || j != nil {
		t.Errorf("next = %v, %v", j, err)
	}
	if got, err := mod.Get(ctx, ada, a.ID); err != nil || !got.Online {
		t.Errorf("after polling = %+v, %v", got, err)
	}
	// Agents are their owner's alone.
	if _, err := mod.Get(ctx, eve, a.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("eve gets ada's agent: %v", err)
	}
	if _, err := mod.Next(ctx, eve, a.ID, 0); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("eve polls ada's agent: %v", err)
	}
	if _, err := mod.Runner(ctx, eve, a.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("eve sends through ada's agent: %v", err)
	}
	if list, _ := mod.List(ctx, eve); len(list) != 0 {
		t.Errorf("eve lists %v", list)
	}
	if err := mod.Delete(ctx, eve, a.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("eve deletes: %v", err)
	}
	if err := mod.Delete(ctx, ada, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.Next(ctx, ada, a.ID, 0); !errors.Is(err, agents.ErrNotFound) {
		t.Errorf("a deleted agent polls: %v", err)
	}
}

func TestRunThroughAnAgent(t *testing.T) {
	ctx, mod, ada, _ := setup(t)
	reg := metrics.NewRegistry()
	mod.UseMetrics(reg, runner.NewMetrics(reg))
	if got := scrape(reg); !strings.Contains(got, "signalbox_agents_online 0\n") {
		t.Errorf("before connecting:\n%s", got)
	}
	a, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mod.Runner(ctx, ada, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The agent: takes jobs and answers with the URL it was asked for.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 2 {
			j, err := mod.Next(ctx, ada, a.ID, time.Second)
			if err != nil || j == nil {
				t.Errorf("next = %v, %v", j, err)
				return
			}
			// The agent gives up when the server would.
			if j.Request.Timeout != 30*time.Second {
				t.Errorf("a job's timeout is %s", j.Request.Timeout)
			}
			res := runner.Result{Response: &runner.Response{Status: 200, Body: []byte(j.Request.URL)}}
			if err := mod.Complete(ctx, ada, a.ID, j.ID, res); err != nil {
				t.Error(err)
			}
			// A second result for the job is refused.
			if err := mod.Complete(ctx, ada, a.ID, j.ID, res); !errors.Is(err, agents.ErrNotFound) {
				t.Errorf("second result: %v", err)
			}
		}
	}()
	for _, u := range []string{"http://localhost:3000/a", "http://localhost:3000/b"} {
		res := r.Run(ctx, runner.Request{Method: "GET", URL: u})
		if res.Response == nil || string(res.Response.Body) != u {
			t.Errorf("result = %+v", res)
		}
	}
	wg.Wait()
	got := scrape(reg)
	for _, want := range []string{`signalbox_runner_requests_total{where="agent",outcome="response"} 2`, "signalbox_agents_online 1"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("lacks %s:\n%s", want, got)
		}
	}
}

func scrape(reg *metrics.Registry) string {
	var b bytes.Buffer
	reg.WriteTo(&b)
	return b.String()
}

func TestAnOfflineAgent(t *testing.T) {
	ctx, mod, ada, _ := setup(t)
	agents.SetPickUp(mod, 50*time.Millisecond)
	a, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop", Hostname: "ada-mbp"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mod.Runner(ctx, ada, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	res := r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/"})
	if res.Failure == nil || res.Failure.Code != "agent-offline" || !strings.Contains(res.Failure.Message, "ada-mbp") {
		t.Fatalf("result = %+v", res)
	}
	// The job it gave up on isn't handed out later.
	if j, err := mod.Next(ctx, ada, a.ID, 0); err != nil || j != nil {
		t.Errorf("a given-up job was handed out: %v, %v", j, err)
	}
	// A cancelled send stops waiting.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if res := r.Run(cctx, runner.Request{Method: "GET", URL: "http://localhost/"}); res.Failure == nil || res.Failure.Code != "timeout" {
		t.Errorf("cancelled = %+v", res)
	}
}

func TestAnAgentThatTakesAJobButNeverAnswers(t *testing.T) {
	ctx, mod, ada, _ := setup(t)
	agents.SetPickUp(mod, time.Second)
	agents.SetGrace(mod, 0)
	a, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mod.Runner(ctx, ada, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	taken := make(chan *agents.Job, 1)
	go func() {
		j, err := mod.Next(ctx, ada, a.ID, time.Second)
		if err != nil || j == nil {
			t.Errorf("next = %v, %v", j, err)
		}
		taken <- j
	}()
	res := r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/", Timeout: 50 * time.Millisecond})
	if res.Failure == nil || res.Failure.Code != "agent-timeout" {
		t.Fatalf("result = %+v", res)
	}
	// Its result, when it comes, has no one to go to.
	if j := <-taken; j != nil {
		if err := mod.Complete(ctx, ada, a.ID, j.ID, runner.Result{}); !errors.Is(err, agents.ErrNotFound) {
			t.Errorf("a late result: %v", err)
		}
	}
}

func TestABusyAgent(t *testing.T) {
	ctx, mod, ada, _ := setup(t)
	agents.SetPickUp(mod, time.Minute)
	a, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mod.Runner(ctx, ada, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the agent's queue with sends that wait; none is taken.
	cctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range agents.Queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(cctx, runner.Request{Method: "GET", URL: "http://localhost/"})
		}()
	}
	for deadline := time.Now().Add(5 * time.Second); agents.Waiting(mod, a.ID) < agents.Queued; {
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs queued", agents.Waiting(mod, a.ID))
		}
		time.Sleep(time.Millisecond)
	}
	if res := r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/"}); res.Failure == nil || res.Failure.Code != "agent-busy" {
		t.Errorf("a full queue gave %+v", res)
	}
	// Once those senders give up, their jobs make no room problem: the
	// next send waits for the agent as usual.
	cancel()
	wg.Wait()
	agents.SetPickUp(mod, 50*time.Millisecond)
	if res := r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/"}); res.Failure == nil || res.Failure.Code != "agent-offline" {
		t.Errorf("after the senders gave up: %+v", res)
	}
}

func TestAnAgentSendingIsBusyNotOffline(t *testing.T) {
	ctx, mod, ada, _ := setup(t)
	agents.SetPickUp(mod, 100*time.Millisecond)
	a, err := mod.Connect(ctx, ada, agents.Input{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := mod.Runner(ctx, ada, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The agent takes one request and is still sending it.
	taken := make(chan *agents.Job, 1)
	go func() {
		j, err := mod.Next(ctx, ada, a.ID, time.Second)
		if err != nil || j == nil {
			t.Errorf("next = %v, %v", j, err)
		}
		taken <- j
	}()
	first := make(chan runner.Result, 1)
	go func() {
		first <- r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/slow", Timeout: 5 * time.Second})
	}()
	j := <-taken
	if j == nil {
		t.FailNow()
	}

	// It asks for no more work meanwhile, yet counts as online.
	agents.ForgetPolls(mod)
	if got, err := mod.Get(ctx, ada, a.ID); err != nil || !got.Online {
		t.Errorf("while sending: %+v, %v", got, err)
	}
	res := r.Run(ctx, runner.Request{Method: "GET", URL: "http://localhost/next"})
	if res.Failure == nil || res.Failure.Code != "agent-busy" || !strings.Contains(res.Failure.Message, "sending 1 other") {
		t.Errorf("a request it had no room for: %+v", res)
	}

	if err := mod.Complete(ctx, ada, a.ID, j.ID, runner.Result{Response: &runner.Response{Status: 200}}); err != nil {
		t.Fatal(err)
	}
	if res := <-first; res.Response == nil {
		t.Errorf("first = %+v", res)
	}
	if got, _ := mod.Get(ctx, ada, a.ID); got.Online {
		t.Error("an agent that neither asks nor sends is online")
	}
}
