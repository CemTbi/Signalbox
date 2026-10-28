// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/client/convert"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
)

func TestImportAndExportACollection(t *testing.T) {
	f := setup(t)
	data, err := os.ReadFile("convert/testdata/shop.postman_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	in, _, err := convert.ReadPostmanCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	viewer := f.member(t, "linus", workspaces.Viewer)
	if _, err := f.mod.ImportCollection(f.ctx, viewer, f.project, in); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer import: %v", err)
	}
	c, err := f.mod.ImportCollection(f.ctx, f.owner, f.project, in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Shop API" || len(c.Variables) != 2 || c.Scripts.PreRequest == "" {
		t.Errorf("collection = %+v", c)
	}
	items, err := f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil || len(items) != 7 {
		t.Fatalf("items = %d, %v", len(items), err)
	}
	// Folders and requests keep their order.
	if items[0].Name != "Orders" || items[1].Name != "Sign in" || items[5].Name != "Get an order" || items[6].Name != "Create an order" || items[6].ParentID != items[0].ID {
		t.Errorf("order = %v", []string{items[0].Name, items[1].Name, items[5].Name, items[6].Name})
	}
	get, err := f.mod.GetItem(f.ctx, f.owner, items[5].ID)
	if err != nil || get.Request.Auth == nil || get.Request.Auth.Type != "apikey" || get.Scripts.Test == "" {
		t.Errorf("get = %+v %+v, %v", get.Request, get.Scripts, err)
	}

	// The export is the same collection.
	out, err := f.mod.ExportCollection(f.ctx, viewer, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("export differs:\n%+v\n%+v", out, in)
	}

	// Its variables fill in {{name}}, and the environment's win.
	staging := f.environments(t)["staging"]
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{{Key: "baseUrl", Value: ptr("https://staging.example.com")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "{{baseUrl}}/orders/{{orderId}}"}, client.SendOptions{CollectionID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if got := f.run.got[len(f.run.got)-1].URL; got != "https://api.example.com/orders/42" {
		t.Errorf("with the collection: %q", got)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "{{baseUrl}}/orders/{{orderId}}"}, client.SendOptions{CollectionID: c.ID, EnvironmentID: staging}); err != nil {
		t.Fatal(err)
	}
	if got := f.run.got[len(f.run.got)-1].URL; got != "https://staging.example.com/orders/42" {
		t.Errorf("with the environment too: %q", got)
	}
	other, err := f.ws.CreateProject(f.ctx, f.owner, f.workspace(t), workspaces.ProjectInput{Name: "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Send(f.ctx, f.owner, other.ID, runner.Request{Method: "GET", URL: "https://a.test/"}, client.SendOptions{CollectionID: c.ID}); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("another project's collection: %v", err)
	}

	// Collection variables can be changed.
	vars := []client.CollectionVariable{{Key: "baseUrl", Value: "https://v2.example.com"}}
	if c, err = f.mod.UpdateCollection(f.ctx, f.owner, c.ID, workspaces.Precondition{}, client.CollectionPatch{Variables: &vars}); err != nil || !reflect.DeepEqual(c.Variables, vars) {
		t.Errorf("update variables = %+v, %v", c.Variables, err)
	}
	huge := make([]client.CollectionVariable, 40)
	for i := range huge {
		huge[i] = client.CollectionVariable{Key: fmt.Sprintf("v%d", i), Value: strings.Repeat("x", 60<<10)}
	}
	if _, err := f.mod.UpdateCollection(f.ctx, f.owner, c.ID, workspaces.Precondition{}, client.CollectionPatch{Variables: &huge}); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Errorf("2.4 MiB of variables: %v", err)
	}
	bad := []client.CollectionVariable{{Key: "a b"}, {Key: "x"}, {Key: "x"}}
	if _, err := f.mod.UpdateCollection(f.ctx, f.owner, c.ID, workspaces.Precondition{}, client.CollectionPatch{Variables: &bad}); err == nil {
		t.Error("invalid variables were stored")
	}
}

func TestImportEnvironmentVariables(t *testing.T) {
	f := setup(t)
	withKey(t, f)
	staging := f.environments(t)["staging"]
	if _, err := f.mod.PutVariables(f.ctx, f.owner, staging, workspaces.Precondition{}, []client.VariableInput{
		{Key: "baseUrl", Value: ptr("https://old.example.com")},
		{Key: "kept", Value: ptr("1")},
		{Key: "token", Value: ptr("old-secret"), Secret: true},
	}); err != nil {
		t.Fatal(err)
	}
	vs, err := f.mod.ImportVariables(f.ctx, f.owner, staging, []convert.Variable{
		{Key: "baseUrl", Value: "https://staging.example.com"},
		{Key: "apiKey", Value: "k-1", Secret: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []client.Variable{{Key: "baseUrl", Value: "https://staging.example.com"}, {Key: "kept", Value: "1"}, {Key: "token", Secret: true}, {Key: "apiKey", Secret: true}}
	if !reflect.DeepEqual(vs.Items, want) {
		t.Errorf("variables = %+v", vs.Items)
	}
	// The existing secret kept its value.
	if _, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "https://a.test/{{token}}/{{apiKey}}"}, client.SendOptions{EnvironmentID: staging}); err != nil {
		t.Fatal(err)
	}
	if got := f.run.got[len(f.run.got)-1].URL; got != "https://a.test/old-secret/k-1" {
		t.Errorf("url = %q", got)
	}
}
