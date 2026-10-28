// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
)

func engine(t *testing.T, run client.Runner) *client.Engine {
	t.Helper()
	var e *client.Engine
	pool := scripts.NewPool(1, scripts.Options{Send: func(ctx context.Context, req scripts.Request) (*scripts.Response, error) {
		return e.ScriptSender()(ctx, req)
	}, MaxMemory: -1})
	t.Cleanup(pool.Close)
	e = client.NewEngine(run, pool)
	return e
}

func step(name, url string, s client.Scripts) client.Step {
	return client.Step{Name: name, Request: runner.Request{Method: "GET", URL: url}, Chain: []client.Link{{From: name, Scripts: s}}}
}

func runAll(t *testing.T, e *client.Engine, p client.Plan, opts client.RunOptions) ([]client.StepResult, string) {
	t.Helper()
	var got []client.StepResult
	stopped, err := e.Run(context.Background(), p, opts, func(r client.StepResult) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, stopped
}

func TestRunCarriesVariablesAcrossStepsAndIterations(t *testing.T) {
	run := &fakeRunner{body: []byte(`{"token":"tok-from-login"}`)}
	e := engine(t, run)
	p := client.Plan{
		Variables: map[string]string{"base": "https://api.example.com"},
		Steps: []client.Step{
			step("Log in", "{{base}}/login?user={{user}}", client.Scripts{Test: `
				pm.environment.set("session", pm.response.json().token);
				pm.collectionVariables.set("count", (Number(pm.collectionVariables.get("count")) || 0) + 1);
				pm.test("iteration " + pm.info.iteration + " of " + pm.info.iterationCount, () => {});`}),
			step("Orders", "{{base}}/orders?count={{count}}", client.Scripts{PreRequest: `pm.variables.set("local", "x");`}),
			{Name: "Profile", Request: runner.Request{Method: "GET", URL: "{{base}}/me?local={{local}}"},
				Auth: &client.Auth{Type: client.AuthBearer, Token: "{{session}}"}},
		},
	}
	got, stopped := runAll(t, e, p, client.RunOptions{
		Data:        []map[string]any{{"user": "ada"}, {"user": "bob"}},
		Environment: map[string]string{"apiKey": "k-123"}, Secrets: []string{"session"},
	})
	if stopped != "" || len(got) != 6 {
		t.Fatalf("got %d steps, stopped %q", len(got), stopped)
	}
	urls := make([]string, len(run.got))
	for i, r := range run.got {
		urls[i] = r.URL
	}
	want := []string{
		"https://api.example.com/login?user=ada", "https://api.example.com/orders?count=1", "https://api.example.com/me?local={{local}}",
		"https://api.example.com/login?user=bob", "https://api.example.com/orders?count=2", "https://api.example.com/me?local={{local}}",
	}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Errorf("sent\n%s\nwant\n%s", strings.Join(urls, "\n"), strings.Join(want, "\n"))
	}
	if got[3].Iteration != 1 || got[3].Index != 0 || got[3].Entry.Scripts.Tests[0].Name != "iteration 1 of 2" {
		t.Errorf("second iteration's login = %+v", got[3])
	}
	// The token a script set fills in auth, and is sent, but isn't secret:
	// only environment variables marked secret are masked.
	if h := run.got[2].Headers; len(h) != 1 || h[0].Value != "Bearer tok-from-login" {
		t.Errorf("profile headers = %v", h)
	}
	if h := got[2].Entry.Headers; h[0].Value != "•••" {
		t.Errorf("kept profile headers = %v", h)
	}
	// Local values last for one request.
	if got[2].Entry.Unresolved[0] != "local" {
		t.Errorf("unresolved = %v", got[2].Entry.Unresolved)
	}
}

func TestRunMasksSecretsScriptsSee(t *testing.T) {
	e := engine(t, &fakeRunner{body: []byte(`{}`)})
	p := client.Plan{Steps: []client.Step{step("Log", "https://a.test/?k={{apiKey}}", client.Scripts{
		Test: `console.log("key", pm.environment.get("apiKey")); pm.environment.set("apiKey", "changed-key"); pm.test("t " + pm.environment.get("apiKey"), () => {});`})}}
	got, _ := runAll(t, e, p, client.RunOptions{Iterations: 2, Environment: map[string]string{"apiKey": "k-123"}, Secrets: []string{"apiKey"}})
	for _, r := range got {
		e := r.Entry
		if strings.Contains(e.URL, "k-123") || strings.Contains(e.Scripts.Logs[0].Message, "k-123") {
			t.Errorf("kept a secret: %s %q", e.URL, e.Scripts.Logs[0].Message)
		}
	}
	// The second iteration sends what the first set, and masks it too.
	if u := got[1].Entry.URL; u != "https://a.test/?k=•••" {
		t.Errorf("second URL = %s", u)
	}
	if n := got[1].Entry.Scripts.Tests[0].Name; n != "t •••" {
		t.Errorf("test name = %s", n)
	}
}

func TestSetNextRequest(t *testing.T) {
	for _, tt := range []struct {
		name, script string
		want         []string
		note         bool
	}{
		{"jumps ahead", `pm.execution.setNextRequest("C");`, []string{"A", "C"}, false},
		{"null ends the iteration", `postman.setNextRequest(null);`, []string{"A", "A"}, false},
		{"unknown names end it too", `pm.execution.setNextRequest("Nope");`, []string{"A", "A"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := engine(t, &fakeRunner{body: []byte(`{}`)})
			p := client.Plan{Steps: []client.Step{
				step("A", "https://a.test/a", client.Scripts{Test: tt.script}),
				step("B", "https://a.test/b", client.Scripts{}),
				step("C", "https://a.test/c", client.Scripts{Test: `pm.execution.setNextRequest(null);`}),
			}}
			got, _ := runAll(t, e, p, client.RunOptions{Iterations: 2})
			names := []string{}
			for _, r := range got {
				names = append(names, p.Steps[r.Index].Name)
			}
			if tt.name == "jumps ahead" {
				tt.want = append(tt.want, tt.want...)
			}
			if fmt.Sprint(names) != fmt.Sprint(tt.want) {
				t.Errorf("ran %v, want %v", names, tt.want)
			}
			if (got[0].Note != "") != tt.note {
				t.Errorf("note = %q", got[0].Note)
			}
		})
	}
}

func TestBailStopsAtTheFirstFailure(t *testing.T) {
	run := &fakeRunner{body: []byte(`{}`)}
	e := engine(t, run)
	p := client.Plan{Steps: []client.Step{
		step("Skipped", "https://a.test/0", client.Scripts{PreRequest: `pm.execution.skipRequest();`}),
		step("Passes", "https://a.test/1", client.Scripts{Test: `pm.test("ok", () => {});`}),
		step("Fails", "https://a.test/2", client.Scripts{Test: `pm.test("status", () => pm.response.to.have.status(201));`}),
		step("Never", "https://a.test/3", client.Scripts{}),
	}}
	got, stopped := runAll(t, e, p, client.RunOptions{Bail: true})
	if stopped != client.StoppedBail || len(got) != 3 {
		t.Fatalf("stopped %q after %d steps", stopped, len(got))
	}
	if got[0].Failed() || got[1].Failed() || !got[2].Failed() {
		t.Errorf("failed = %v %v %v", got[0].Failed(), got[1].Failed(), got[2].Failed())
	}
	if len(run.got) != 2 {
		t.Errorf("sent %d requests", len(run.got))
	}
	// Without bail, the run goes on.
	if got, stopped := runAll(t, e, p, client.RunOptions{}); stopped != "" || len(got) != 4 {
		t.Errorf("without bail: stopped %q after %d steps", stopped, len(got))
	}
}

func TestRunStopsWhenAsked(t *testing.T) {
	e := engine(t, &fakeRunner{body: []byte(`{}`)})
	p := client.Plan{Steps: []client.Step{{Name: "A", Request: runner.Request{Method: "GET", URL: "https://a.test/"}}}}
	n := 0
	_, err := e.Run(context.Background(), p, client.RunOptions{Iterations: 5}, func(client.StepResult) error {
		n++
		return client.ErrStopRun
	})
	if !errors.Is(err, client.ErrStopRun) || n != 1 {
		t.Errorf("err = %v after %d steps", err, n)
	}
	// Without a runner nothing runs.
	if _, err := client.NewEngine(nil, nil).Run(context.Background(), p, client.RunOptions{}, nil); !errors.Is(err, client.ErrRunnerUnavailable) {
		t.Errorf("no runner: %v", err)
	}
}

func TestPlanRun(t *testing.T) {
	f := setup(t)
	c, err := f.mod.ImportCollection(f.ctx, f.owner, f.project, convert.Collection{
		Name:      "Shop API",
		Variables: []convert.Variable{{Key: "base", Value: "https://api.example.com"}},
		Scripts:   convert.Scripts{PreRequest: "// collection"},
		Items: []convert.Item{
			{Name: "Health", Request: &convert.Request{Method: "GET", URL: "{{base}}/health"}},
			{Name: "Orders", Scripts: convert.Scripts{Test: "// orders"}, Items: []convert.Item{
				{Name: "Create", Request: &convert.Request{Method: "POST", URL: "{{base}}/orders", Body: `{"sku":"A-1"}`,
					Headers: []convert.Header{{Key: "X-On", Value: "1", Enabled: true}, {Key: "X-Off", Value: "1"}}}},
				{Name: "Refunds", Items: []convert.Item{{Name: "Refund", Request: &convert.Request{Method: "POST", URL: "{{base}}/refunds"}}}},
			}},
			{Name: "Logout", Request: &convert.Request{Method: "POST", URL: "{{base}}/logout"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	col, p, err := f.mod.PlanRun(f.ctx, f.owner, c.ID, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range p.Steps {
		var from []string
		for _, l := range s.Chain {
			from = append(from, l.From)
		}
		got = append(got, s.Path+"|"+s.Name+"|"+strings.Join(from, ">"))
	}
	want := []string{
		"|Health|Shop API>Health",
		"Orders|Create|Shop API>Orders>Create",
		"Orders / Refunds|Refund|Shop API>Orders>Refunds>Refund",
		"|Logout|Shop API>Logout",
	}
	if col.Name != "Shop API" || p.Variables["base"] != "https://api.example.com" || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("plan = %v, %v", got, p.Variables)
	}
	if r := p.Steps[1].Request; len(r.Headers) != 1 || r.Headers[0].Key != "X-On" || string(r.Body) != `{"sku":"A-1"}` {
		t.Errorf("create = %+v", r)
	}

	// A folder runs with the scripts above it.
	items, err := f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var orders, health uuid.UUID
	for _, it := range items {
		switch it.Name {
		case "Orders":
			orders = it.ID
		case "Health":
			health = it.ID
		}
	}
	_, p, err = f.mod.PlanRun(f.ctx, f.owner, c.ID, orders)
	if err != nil || p.Name != "Orders" || len(p.Steps) != 2 || p.Steps[1].Path != "Orders / Refunds" || len(p.Steps[0].Chain) != 3 {
		t.Errorf("folder plan = %+v, %v", p, err)
	}
	// A request isn't a folder, and viewers can't run.
	if _, _, err := f.mod.PlanRun(f.ctx, f.owner, c.ID, health); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("request as folder: %v", err)
	}
	viewer := f.member(t, "linus", workspaces.Viewer)
	if _, _, err := f.mod.PlanRun(f.ctx, viewer, c.ID, uuid.Nil); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
}

func TestPlanCollection(t *testing.T) {
	c := convert.Collection{
		Name:      "Shop API",
		Variables: []convert.Variable{{Key: "base", Value: "https://api.example.com"}},
		Scripts:   convert.Scripts{PreRequest: "// collection"},
		Items: []convert.Item{
			{Name: "Health", Request: &convert.Request{Method: "GET", URL: "{{base}}/health", Auth: &convert.Auth{Type: client.AuthBearer, Token: "{{token}}"}}},
			{Name: "Orders", Items: []convert.Item{
				{Name: "Create", Request: &convert.Request{Method: "POST", URL: "{{base}}/orders", Headers: []convert.Header{{Key: "X-On", Value: "1", Enabled: true}, {Key: "X-Off", Value: "1"}}}},
				{Name: "Refunds", Items: []convert.Item{{Name: "Refund", Request: &convert.Request{Method: "POST", URL: "{{base}}/refunds"}}}},
			}},
		},
	}
	p, err := client.PlanCollection(c, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 3 || p.Steps[2].Path != "Orders / Refunds" || len(p.Steps[2].Chain) != 4 || p.Variables["base"] == "" {
		t.Errorf("plan = %+v", p)
	}
	if a := p.Steps[0].Auth; a == nil || a.Token != "{{token}}" || len(p.Steps[1].Request.Headers) != 1 {
		t.Errorf("health auth = %+v, create headers = %v", a, p.Steps[1].Request.Headers)
	}
	p, err = client.PlanCollection(c, "Refunds")
	if err != nil || p.Name != "Refunds" || len(p.Steps) != 1 || p.Steps[0].Path != "Orders / Refunds" || len(p.Steps[0].Chain) != 4 {
		t.Errorf("folder plan = %+v, %v", p, err)
	}
	if _, err := client.PlanCollection(c, "Health"); !errors.Is(err, client.ErrNoFolder) {
		t.Errorf("a request isn't a folder: %v", err)
	}
}
