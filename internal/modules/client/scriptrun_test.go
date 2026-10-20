// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/scripts"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
)

func withScripts(t *testing.T, f fixture) {
	t.Helper()
	pool := scripts.NewPool(1, scripts.Options{Send: f.mod.ScriptSender(), MaxMemory: -1})
	t.Cleanup(pool.Close)
	f.mod.UseScripts(pool)
}

// scripted imports a collection whose collection, folder and request each
// have scripts, and returns the request's ID.
func scripted(t *testing.T, f fixture, request convert.Scripts) uuid.UUID {
	t.Helper()
	c, err := f.mod.ImportCollection(f.ctx, f.owner, f.project, convert.Collection{
		Name:      "Shop API",
		Variables: []convert.Variable{{Key: "baseUrl", Value: "https://api.example.com"}},
		Scripts:   convert.Scripts{PreRequest: `pm.variables.set("trace", "abc");`},
		Items: []convert.Item{{Name: "Orders", Scripts: convert.Scripts{PreRequest: `pm.request.headers.add({key: "X-Folder", value: "1"});`}, Items: []convert.Item{{
			Name:    "Get an order",
			Request: &convert.Request{Method: "GET", URL: "{{baseUrl}}/orders/{{orderId}}", Headers: []convert.Header{{Key: "X-Trace", Value: "{{trace}}", Enabled: true}}},
			Scripts: request,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return items[len(items)-1].ID
}

func TestScriptsRunAroundASend(t *testing.T) {
	f := setup(t)
	withKey(t, f)
	withScripts(t, f)
	staging := f.environments(t)["staging"]
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{
		{Key: "orderId", Value: ptr("1")}, {Key: "token", Value: ptr("tok-secret-1"), Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	item := scripted(t, f, convert.Scripts{
		PreRequest: `pm.environment.set("orderId", "7");`,
		Test: `pm.test("is ok", () => pm.response.to.have.status(200));
pm.test("is wrong", () => pm.expect(pm.response.json().ok).to.eql(false));
console.log("token", pm.environment.get("token"));`,
	})

	e, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "{{baseUrl}}/orders/{{orderId}}", Headers: []runner.Header{{Key: "X-Trace", Value: "{{trace}}"}}},
		client.SendOptions{EnvironmentID: staging, ItemID: item})
	if err != nil {
		t.Fatal(err)
	}
	sent := f.run.got[len(f.run.got)-1]
	// The collection's, the folder's and the request's scripts all ran, in
	// that order, before the variables were filled in.
	if sent.URL != "https://api.example.com/orders/7" || header(sent.Headers, "X-Folder")[0] != "1" || header(sent.Headers, "X-Trace")[0] != "abc" {
		t.Errorf("sent = %+v", sent)
	}
	o := e.Scripts
	if o == nil || len(o.Tests) != 2 || !o.Tests[0].Passed || o.Tests[1].Passed || o.Tests[1].From != "Get an order" || len(o.Errors) != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	if len(o.Logs) != 1 || !strings.Contains(o.Logs[0].Message, "tok-secret-1") {
		t.Errorf("logs = %+v", o.Logs)
	}
	if len(o.Changes) == 0 || o.Changes[len(o.Changes)-1].Key != "orderId" {
		t.Errorf("changes = %+v", o.Changes)
	}
	// History has the outcome, without the secret.
	got, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID)
	if err != nil || got.Scripts == nil || len(got.Scripts.Tests) != 2 || strings.Contains(got.Scripts.Logs[0].Message, "tok-secret-1") {
		t.Errorf("history = %+v, %v", got.Scripts, err)
	}
	// What scripts set lasts for the send only.
	vs, _ := f.mod.GetVariables(f.ctx, f.owner, staging)
	if vs.Items[0].Value != "1" {
		t.Errorf("orderId was saved as %q", vs.Items[0].Value)
	}
}

func TestPreRequestScriptsCanStopASend(t *testing.T) {
	f := setup(t)
	withScripts(t, f)
	req := runner.Request{Method: "GET", URL: "https://api.example.com/"}
	before := len(f.run.got)

	e, err := f.mod.Send(f.ctx, f.owner, f.project, req, client.SendOptions{Scripts: &client.Scripts{PreRequest: `throw new Error("no token");`}})
	if err != nil {
		t.Fatal(err)
	}
	if f := e.Result.Failure; f == nil || f.Code != "script" || !strings.Contains(f.Message, "no token") {
		t.Errorf("failure = %+v", e.Result.Failure)
	}
	e, err = f.mod.Send(f.ctx, f.owner, f.project, req, client.SendOptions{Scripts: &client.Scripts{PreRequest: `pm.execution.skipRequest();`}})
	if err != nil {
		t.Fatal(err)
	}
	if f := e.Result.Failure; f == nil || f.Code != "skipped" || !e.Scripts.Skipped {
		t.Errorf("skipped = %+v %+v", e.Result.Failure, e.Scripts)
	}
	if len(f.run.got) != before {
		t.Errorf("%d requests were sent", len(f.run.got)-before)
	}

	// The editor's scripts replace the saved ones.
	item := scripted(t, f, convert.Scripts{Test: `pm.test("saved", () => {});`})
	e, err = f.mod.Send(f.ctx, f.owner, f.project, req, client.SendOptions{ItemID: item, Scripts: &client.Scripts{Test: `pm.test("edited", () => {});`}})
	if err != nil || len(e.Scripts.Tests) != 1 || e.Scripts.Tests[0].Name != "edited" {
		t.Errorf("edited scripts = %+v, %v", e.Scripts, err)
	}

	// Another project's request isn't this project's.
	other, err := f.ws.CreateProject(f.ctx, f.owner, f.workspace(t), workspaces.ProjectInput{Name: "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, other.ID, req, client.SendOptions{ItemID: item}); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("another project's item: %v", err)
	}
}

func TestScriptsNeedAPool(t *testing.T) {
	f := setup(t)
	_, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "https://a.test/"}, client.SendOptions{Scripts: &client.Scripts{Test: "pm.test('x', () => {})"}})
	if !errors.Is(err, client.ErrScriptsUnavailable) {
		t.Errorf("err = %v", err)
	}
	// Empty scripts need none.
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "https://a.test/"}, client.SendOptions{Scripts: &client.Scripts{}}); err != nil {
		t.Errorf("empty scripts: %v", err)
	}
}
