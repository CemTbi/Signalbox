// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/secretbox"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func ptr(s string) *string { return &s }

// environments returns the project's environments by slug.
func (f fixture) environments(t *testing.T) map[string]uuid.UUID {
	t.Helper()
	envs, err := f.ws.ListEnvironments(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]uuid.UUID{}
	for _, e := range envs {
		out[e.Slug] = e.ID
	}
	return out
}

func withKey(t *testing.T, f fixture) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(secretbox.NewKey())
	if err != nil {
		t.Fatal(err)
	}
	f.mod.UseSecretKey(box)
	return box
}

func TestVariablesAndSecrets(t *testing.T) {
	f := setup(t)
	withKey(t, f)
	staging := f.environments(t)["staging"]

	empty, err := f.mod.GetVariables(f.ctx, f.owner, staging)
	if err != nil || len(empty.Items) != 0 || empty.Version != 0 {
		t.Fatalf("before = %+v, %v", empty, err)
	}
	vs, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{Versions: []int64{0}}, []client.VariableInput{
		{Key: "baseUrl", Value: ptr("https://staging.example.com")},
		{Key: "token", Value: ptr("tok-123456"), Secret: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if vs.Version != 1 || len(vs.Items) != 2 || vs.Items[0].Value != "https://staging.example.com" || !vs.Items[1].Secret || vs.Items[1].Value != "" {
		t.Errorf("put = %+v", vs)
	}
	// The secret is encrypted at rest.
	var stored []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT secret_value FROM environment_variables WHERE key = 'token'`).Scan(&stored); err != nil || strings.Contains(string(stored), "tok-123456") {
		t.Errorf("stored secret %q, %v", stored, err)
	}
	// A stale version is refused.
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{Versions: []int64{0}}, nil); !errors.Is(err, client.ErrVersionMismatch) {
		t.Errorf("stale put: %v", err)
	}
	// A secret sent without a value keeps its value, under a new position.
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{
		{Key: "token", Secret: true},
		{Key: "baseUrl", Value: ptr("https://staging.example.com")},
	}); err != nil {
		t.Fatal(err)
	}

	e, err := f.mod.Send(f.ctx, f.owner, f.project, staging, runner.Request{
		Method: "GET", URL: "{{baseUrl}}/orders?key={{token}}",
		Headers: []runner.Header{{Key: "X-Api-Key", Value: "{{token}}"}, {Key: "X-Missing", Value: "{{nope}}"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sent := f.run.got[len(f.run.got)-1]
	if sent.URL != "https://staging.example.com/orders?key=tok-123456" || sent.Headers[0].Value != "tok-123456" || sent.Headers[1].Value != "{{nope}}" {
		t.Errorf("runner got %+v", sent)
	}
	if len(e.Unresolved) != 1 || e.Unresolved[0] != "nope" || e.EnvironmentID != staging {
		t.Errorf("entry = %+v", e)
	}
	// History masks the secret wherever it went, the response included.
	got, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://staging.example.com/orders?key=•••" || got.Headers[0].Value != "•••" || got.Result.Response.URL != got.URL ||
		got.EnvironmentID != staging || len(got.Unresolved) != 1 {
		t.Errorf("history = %+v", got)
	}
	var raw string
	if err := f.pool.QueryRow(f.ctx, `SELECT url || record::text FROM request_history`).Scan(&raw); err != nil || strings.Contains(raw, "tok-123456") {
		t.Errorf("history holds the secret: %s, %v", raw, err)
	}

	// Failure messages name the host, which the secret may fill in.
	f.run.fail = func(req runner.Request) *runner.Failure {
		return &runner.Failure{Code: "dns", Message: "There's no host called " + strings.TrimPrefix(req.URL, "https://") + "."}
	}
	failed, err := f.mod.Send(f.ctx, f.owner, f.project, staging, runner.Request{Method: "GET", URL: "https://{{token}}.example.com"}, nil)
	if err != nil || !strings.Contains(failed.Result.Failure.Message, "tok-123456") {
		t.Fatalf("failed send = %+v, %v", failed.Result.Failure, err)
	}
	if got, err := f.mod.HistoryEntry(f.ctx, f.owner, failed.ID); err != nil || got.Result.Failure.Message != "There's no host called •••.example.com." {
		t.Errorf("stored failure = %+v, %v", got.Result.Failure, err)
	}
	f.run.fail = nil
	if err := f.pool.QueryRow(f.ctx, `SELECT string_agg(url || record::text, '') FROM request_history`).Scan(&raw); err != nil || strings.Contains(raw, "tok-123456") {
		t.Errorf("history holds the secret: %s, %v", raw, err)
	}

	// Another project's environment isn't this project's.
	other, err := f.ws.CreateProject(f.ctx, f.owner, f.workspace(t), workspaces.ProjectInput{Name: "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, other.ID, staging, runner.Request{Method: "GET", URL: "https://example.com/"}, nil); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("send with another project's environment: %v", err)
	}
}

func (f fixture) workspace(t *testing.T) uuid.UUID {
	t.Helper()
	p, err := f.ws.GetProject(f.ctx, f.owner, f.project)
	if err != nil {
		t.Fatal(err)
	}
	return p.WorkspaceID
}

func TestVariableRules(t *testing.T) {
	f := setup(t)
	envs := f.environments(t)
	dev := envs["development"]
	viewer := f.member(t, "linus", workspaces.Viewer)
	stranger := f.user(t, "eve")

	if _, err := f.mod.PutVariables(f.ctx, viewer, dev, workspaces.Precondition{}, nil); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer put: %v", err)
	}
	if _, err := f.mod.GetVariables(f.ctx, viewer, dev); err != nil {
		t.Errorf("viewer get: %v", err)
	}
	if _, err := f.mod.GetVariables(f.ctx, stranger, dev); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("stranger get: %v", err)
	}
	// Without a secret key, plain variables work and secrets don't.
	if _, err := f.mod.PutVariables(f.ctx, f.owner, dev, workspaces.Precondition{}, []client.VariableInput{{Key: "a", Value: ptr("1")}}); err != nil {
		t.Errorf("plain without a key: %v", err)
	}
	if _, err := f.mod.PutVariables(f.ctx, f.owner, dev, workspaces.Precondition{}, []client.VariableInput{{Key: "s", Value: ptr("1"), Secret: true}}); !errors.Is(err, client.ErrNoSecretKey) {
		t.Errorf("secret without a key: %v", err)
	}

	var fields validate.Errors
	_, err := f.mod.PutVariables(f.ctx, f.owner, dev, workspaces.Precondition{}, []client.VariableInput{
		{Key: ""}, {Key: "has space"}, {Key: "$guid"}, {Key: "dup"}, {Key: "dup"}, {Key: "new", Secret: true},
		{Key: "big", Value: ptr(strings.Repeat("x", 65537))},
	})
	if !errors.As(err, &fields) {
		t.Fatalf("invalid put: %v", err)
	}
	codes := map[string]string{}
	for _, fe := range fields {
		codes[fe.Pointer] = fe.Code
	}
	want := map[string]string{"/variables/0/key": "required", "/variables/1/key": "pattern", "/variables/2/key": "pattern", "/variables/4/key": "unique", "/variables/6/value": "maxLength"}
	for k, v := range want {
		if codes[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, codes[k], v, fields)
		}
	}

	// A secret sent without a value must already exist.
	withKey(t, f)
	_, err = f.mod.PutVariables(f.ctx, f.owner, dev, workspaces.Precondition{}, []client.VariableInput{{Key: "new", Secret: true}, {Key: "x", Value: ptr("1"), Secret: true}})
	if !errors.As(err, &fields) || fields[0].Pointer != "/variables/0/value" {
		t.Errorf("new secret without a value: %v", err)
	}
}

func TestChangedSecretKey(t *testing.T) {
	f := setup(t)
	withKey(t, f)
	prod := f.environments(t)["production"]
	if _, err := f.mod.PutVariables(f.ctx, f.owner, prod, workspaces.Precondition{}, []client.VariableInput{{Key: "token", Value: ptr("abc"), Secret: true}}); err != nil {
		t.Fatal(err)
	}
	// Another key can't read the workspace's secrets, and says why.
	withKey(t, f)
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, prod, runner.Request{Method: "GET", URL: "https://example.com/{{token}}"}, nil); !errors.Is(err, client.ErrSecretKeyChanged) {
		t.Errorf("send: %v", err)
	}
	// Plain variables still read.
	if _, err := f.mod.GetVariables(f.ctx, f.owner, prod); err != nil {
		t.Errorf("get: %v", err)
	}
}
