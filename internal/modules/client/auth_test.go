// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func header(hs []runner.Header, name string) []string {
	var out []string
	for _, h := range hs {
		if strings.EqualFold(h.Key, name) {
			out = append(out, h.Value)
		}
	}
	return out
}

func TestAuthIsAppliedAfterVariables(t *testing.T) {
	f := setup(t)
	withKey(t, f)
	staging := f.environments(t)["staging"]
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{
		{Key: "user", Value: ptr("ada")},
		{Key: "password", Value: ptr("pa55-w0rd"), Secret: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Basic auth encodes the filled-in username and password, and replaces
	// an Authorization header the request has.
	req := runner.Request{Method: "GET", URL: "https://api.example.com/me", Headers: []runner.Header{{Key: "authorization", Value: "stale"}, {Key: "Accept", Value: "*/*"}}}
	e, err := f.mod.Send(f.ctx, f.owner, f.project, staging, req, &client.Auth{Type: "basic", Username: "{{user}}", Password: "{{password}}"})
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("ada:pa55-w0rd"))
	sent := f.run.got[len(f.run.got)-1]
	if got := header(sent.Headers, "Authorization"); len(got) != 1 || got[0] != "Basic "+encoded {
		t.Errorf("authorization = %v", got)
	}
	if e.Auth == nil || e.Auth.Type != "basic" || e.Auth.Username != "ada" || e.Auth.Password != "" {
		t.Errorf("entry auth = %+v", e.Auth)
	}

	// An API key in the query is escaped, and history masks it even when it
	// isn't a secret variable.
	f.run.body = []byte(`{"echo":"k&y=1"}`)
	e, err = f.mod.Send(f.ctx, f.owner, f.project, uuid.Nil, runner.Request{Method: "GET", URL: "https://api.example.com/orders?page=2"},
		&client.Auth{Type: "apikey", Key: "api_key", Value: "k&y=1", In: "query"})
	if err != nil {
		t.Fatal(err)
	}
	if sent := f.run.got[len(f.run.got)-1]; sent.URL != "https://api.example.com/orders?page=2&api_key=k%26y%3D1" {
		t.Errorf("url = %q", sent.URL)
	}
	got, err := f.mod.HistoryEntry(f.ctx, f.owner, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://api.example.com/orders?page=2&api_key=•••" || string(got.Result.Response.Body) != `{"echo":"•••"}` ||
		got.Auth == nil || got.Auth.Key != "api_key" || got.Auth.Value != "" {
		t.Errorf("history = %q %s %+v", got.URL, got.Result.Response.Body, got.Auth)
	}

	// A bearer token and an API key header.
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, uuid.Nil, runner.Request{Method: "GET", URL: "https://api.example.com/"}, &client.Auth{Type: "Bearer", Token: "t0ken"}); err != nil {
		t.Fatal(err)
	}
	if got := header(f.run.got[len(f.run.got)-1].Headers, "Authorization"); len(got) != 1 || got[0] != "Bearer t0ken" {
		t.Errorf("bearer = %v", got)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, uuid.Nil, runner.Request{Method: "GET", URL: "https://api.example.com/"}, &client.Auth{Type: "apikey", Key: "X-Api-Key", Value: "abc123"}); err != nil {
		t.Fatal(err)
	}
	if got := header(f.run.got[len(f.run.got)-1].Headers, "X-Api-Key"); len(got) != 1 || got[0] != "abc123" {
		t.Errorf("api key header = %v", got)
	}

	var raw string
	if err := f.pool.QueryRow(f.ctx, `SELECT string_agg(url || record::text, '') FROM request_history`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"pa55-w0rd", encoded, "k%26y%3D1", "t0ken", "abc123"} {
		if strings.Contains(raw, secret) {
			t.Errorf("history holds %q", secret)
		}
	}

	// Auth is checked.
	var fields validate.Errors
	_, err = f.mod.Send(f.ctx, f.owner, f.project, uuid.Nil, runner.Request{Method: "GET", URL: "https://api.example.com/"}, &client.Auth{Type: "apikey", In: "cookie"})
	if !errors.As(err, &fields) || len(fields) != 2 {
		t.Errorf("bad api key: %v", err)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, uuid.Nil, runner.Request{Method: "GET", URL: "https://api.example.com/"}, &client.Auth{Type: "digest"}); !errors.As(err, &fields) {
		t.Errorf("digest: %v", err)
	}
}

func TestSavedRequestsKeepAuth(t *testing.T) {
	f := setup(t)
	c, err := f.mod.CreateCollection(f.ctx, f.owner, f.project, "Shop API", "")
	if err != nil {
		t.Fatal(err)
	}
	it, err := f.mod.CreateItem(f.ctx, f.owner, c.ID, client.ItemInput{Kind: client.KindRequest, Name: "Me", Request: &client.SavedRequest{
		Method: "GET", URL: "{{baseUrl}}/me", Auth: &client.Auth{Type: "bearer", Token: "{{token}}", Username: "dropped"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.mod.GetItem(f.ctx, f.owner, it.ID)
	if err != nil || got.Request.Auth == nil || *got.Request.Auth != (client.Auth{Type: "bearer", Token: "{{token}}"}) {
		t.Fatalf("saved auth = %+v, %v", got.Request, err)
	}
	none := &client.SavedRequest{Method: "GET", URL: "https://example.com/", Auth: &client.Auth{Type: "none"}}
	if got, err := f.mod.UpdateItem(f.ctx, f.owner, it.ID, workspaces.Precondition{}, client.ItemPatch{Request: none}); err != nil || got.Request.Auth != nil {
		t.Errorf("no auth = %+v, %v", got.Request, err)
	}
}
