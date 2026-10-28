// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
)

const ordersV1 = `openapi: 3.1.0
info: {title: Orders, version: 1.0.0}
servers: [{url: 'https://api.example.com/v1/'}]
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
security: [{bearer: []}]
paths:
  /orders:
    post:
      tags: [Orders]
      summary: Create an order
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {sku: {type: string}}}
            example: {sku: A-1}
      responses:
        '201':
          description: Created.
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
  /orders/{orderId}:
    get:
      tags: [Orders]
      summary: Get an order
      parameters:
        - {name: orderId, in: path, required: true, schema: {type: integer}, example: 42}
        - {name: expand, in: query, required: true, schema: {type: string, enum: [lines, none]}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
  /health:
    get:
      security: []
      responses:
        '200': {description: Up.}
`

// published publishes a spec in the fixture's project and returns the
// version's ID.
func published(t *testing.T, f fixture, mods *apis.Module, specID uuid.UUID, content string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	if specID == uuid.Nil {
		svc, err := mods.CreateService(f.ctx, f.owner, f.project, apis.ServiceInput{Name: "Orders", Slug: "orders"})
		if err != nil {
			t.Fatal(err)
		}
		spec, _, err := mods.CreateSpec(f.ctx, f.owner, svc.ID, "Orders API", content)
		if err != nil {
			t.Fatal(err)
		}
		specID = spec.ID
	} else {
		spec, err := mods.GetSpec(f.ctx, f.owner, specID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := mods.CreateRevision(f.ctx, f.owner, specID, workspaces.Precondition{Versions: []int64{spec.Version}}, content, "next"); err != nil {
			t.Fatal(err)
		}
	}
	v, err := mods.PublishVersion(f.ctx, f.owner, specID, apis.VersionInput{})
	if err != nil {
		t.Fatal(err)
	}
	return specID, v.ID
}

func TestContractTests(t *testing.T) {
	f := setup(t)
	mods := apis.New(f.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), f.ws)
	f.mod.UseContracts(mods, mods)
	specID, v1 := published(t, f, mods, uuid.Nil, ordersV1)

	c, report, err := f.mod.GenerateContractTests(f.ctx, f.owner, v1, client.ContractOptions{MockURL: "https://signalbox.test/mocks/x/1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Created || report.Requests != 3 || c.Name != "Orders API contract tests" || c.ContractSpecID != specID || c.ContractVersionID != v1 {
		t.Errorf("generated %+v, %+v", c, report)
	}
	vars := map[string]string{}
	for _, v := range c.Variables {
		vars[v.Key] = v.Value
	}
	if vars["baseUrl"] != "https://api.example.com/v1" || vars["orderId"] != "42" {
		t.Errorf("variables = %v", vars)
	}
	_, p, err := f.mod.PlanRun(f.ctx, f.owner, c.ID, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]client.Step{}
	for _, s := range p.Steps {
		byName[s.Name] = s
	}
	create, get, health := byName["Create an order"], byName["Get an order"], byName["GET /health"]
	if create.Path != "Orders" || !strings.Contains(string(create.Request.Body), `"sku": "A-1"`) || create.Request.Headers[0].Value != "application/json" ||
		create.Auth == nil || create.Auth.Token != "{{token}}" || create.Contract == nil || create.Contract.Path != "/orders" {
		t.Errorf("create = %+v", create)
	}
	if get.Request.URL != "{{baseUrl}}/orders/{{orderId}}?expand=lines" {
		t.Errorf("get URL = %s", get.Request.URL)
	}
	if health.Auth != nil || health.Path != "" {
		t.Errorf("health = %+v", health)
	}

	// Sending a generated request checks its response.
	items, err := f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var getID uuid.UUID
	for _, it := range items {
		if it.Name == "Get an order" {
			getID = it.ID
		}
	}
	f.run.body = []byte(`{"id": "not a number"}`)
	e, err := f.mod.Send(f.ctx, f.owner, f.project, runner.Request{Method: "GET", URL: "https://api.example.com/v1/orders/42?expand=lines"}, client.SendOptions{ItemID: getID})
	if err != nil {
		t.Fatal(err)
	}
	if e.Scripts == nil || len(e.Scripts.Tests) != 2 || !e.Scripts.Tests[0].Passed || e.Scripts.Tests[1].Passed || e.Scripts.Tests[1].From != "Contract" {
		t.Fatalf("checks = %+v", e.Scripts)
	}

	// People's additions and values stay when it's generated again.
	if _, err := f.mod.CreateItem(f.ctx, f.owner, c.ID, client.ItemInput{Kind: client.KindRequest, Name: "Mine", Request: &client.SavedRequest{Method: "GET", URL: "{{baseUrl}}/mine"}}); err != nil {
		t.Fatal(err)
	}
	staging := "https://staging.example.com"
	vs := []client.CollectionVariable{{Key: "baseUrl", Value: staging}, {Key: "orderId", Value: "7"}}
	if _, err := f.mod.UpdateCollection(f.ctx, f.owner, c.ID, workspaces.Precondition{}, client.CollectionPatch{Variables: &vs}); err != nil {
		t.Fatal(err)
	}
	v2content := strings.Replace(ordersV1, "  /health:", "  /customers/{customerId}:\n    get:\n      parameters: [{name: customerId, in: path, required: true, schema: {type: string}}]\n      responses: {'200': {description: OK.}}\n  /health:", 1)
	_, v2 := published(t, f, mods, specID, strings.Replace(v2content, "version: 1.0.0", "version: 2.0.0", 1))
	c2, report, err := f.mod.GenerateContractTests(f.ctx, f.owner, v2, client.ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Created || c2.ID != c.ID || report.Requests != 4 || report.Replaced != 4 || c2.ContractVersionID != v2 {
		t.Errorf("again = %+v, %+v", c2, report)
	}
	vars = map[string]string{}
	for _, v := range c2.Variables {
		vars[v.Key] = v.Value
	}
	if vars["baseUrl"] != staging || vars["orderId"] != "7" || vars["customerId"] != "example" {
		t.Errorf("variables after = %v", vars)
	}
	items, err = f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	if len(items) != 6 || items[0].Name != "Mine" {
		t.Errorf("items after = %v", names)
	}

	// Viewers can't generate, and a server without contracts can't at all.
	viewer := f.member(t, "linus", workspaces.Viewer)
	if _, _, err := f.mod.GenerateContractTests(f.ctx, viewer, v2, client.ContractOptions{}); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
	if _, _, err := f.mod.GenerateContractTests(f.ctx, f.owner, uuid.New(), client.ContractOptions{}); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("no version: %v", err)
	}
	bare := client.New(f.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), f.ws, f.run)
	if _, _, err := bare.GenerateContractTests(context.Background(), f.owner, v2, client.ContractOptions{}); !errors.Is(err, client.ErrNoContracts) {
		t.Errorf("no contracts: %v", err)
	}
}
