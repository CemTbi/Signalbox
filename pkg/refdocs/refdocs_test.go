// SPDX-License-Identifier: Apache-2.0

package refdocs_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/refdocs"
)

const shop = `openapi: 3.2.0
info:
  title: Shop API
  version: 2.1.0
  description: Orders and **customers**.
servers:
  - url: https://{region}.shop.example.com/v1
    description: Production
    variables:
      region: {default: eu}
security:
  - bearer: []
tags:
  - name: orders
    summary: Orders
    description: Everything about orders.
  - name: refunds
    parent: orders
  - name: unused
paths:
  /orders/{id}:
    parameters:
      - {name: id, in: path, schema: {type: integer}}
      - {name: X-Trace, in: header, description: Path-level, schema: {type: string}}
    get:
      tags: [orders]
      operationId: getOrder
      summary: Get an order
      parameters:
        - {name: X-Trace, in: header, description: Operation-level, schema: {type: string}}
      responses:
        default:
          description: An error.
        4XX:
          description: Client errors.
        '404':
          description: Not found.
        '200':
          description: The order.
          headers:
            ETag: {description: The order's version., schema: {type: string}}
            Content-Type: {schema: {type: string}}
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
  /orders/{id}/refunds:
    post:
      tags: [refunds]
      security: []
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount: {type: number, exclusiveMinimum: 0, multipleOf: 0.01}
                reason: {type: [string, 'null'], maxLength: 200}
            examples:
              full: {summary: The whole order, value: {amount: 25.5}}
              part: {value: {amount: 5, reason: damaged}}
      responses:
        '201': {description: Refunded.}
  /health:
    get:
      responses:
        '200':
          description: Up.
          content:
            text/plain:
              example: ok
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer, bearerFormat: JWT}
  schemas:
    Order:
      type: object
      required: [id, lines]
      properties:
        id: {type: integer, readOnly: true}
        status: {type: string, enum: [open, paid], default: open}
        lines:
          type: array
          minItems: 1
          items: {$ref: '#/components/schemas/Line'}
        parent: {$ref: '#/components/schemas/Order'}
    Line:
      type: object
      properties:
        sku: {type: string, pattern: '^[A-Z]+$', minLength: 1, maxLength: 1}
`

func build(t *testing.T, doc string) *refdocs.Reference {
	t.Helper()
	ref, err := refdocs.Build([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestGroupsFollowTagsWithUntaggedLast(t *testing.T) {
	ref := build(t, shop)
	if ref.Title != "Shop API" || ref.Version != "2.1.0" || ref.Description != "Orders and **customers**." {
		t.Errorf("info = %q %q %q", ref.Title, ref.Version, ref.Description)
	}
	if len(ref.Servers) != 1 || ref.Servers[0].URL != "https://eu.shop.example.com/v1" {
		t.Errorf("servers = %+v", ref.Servers)
	}
	var names []string
	for _, g := range ref.Groups {
		names = append(names, g.Name)
	}
	// The unused tag is left out; untagged operations come last.
	if !slices.Equal(names, []string{"orders", "refunds", ""}) {
		t.Fatalf("groups = %q", names)
	}
	if g := ref.Groups[0]; g.Summary != "Orders" || g.Description != "Everything about orders." || len(g.Operations) != 1 {
		t.Errorf("orders = %+v", g)
	}
	if ref.Groups[1].Parent != "orders" {
		t.Errorf("refunds parent = %q", ref.Groups[1].Parent)
	}
	if op := ref.Groups[2].Operations[0]; op.ID != "get-/health" || op.Method != http.MethodGet {
		t.Errorf("untagged = %+v", op)
	}
}

func TestOperations(t *testing.T) {
	ref := build(t, shop)
	get := ref.Groups[0].Operations[0]
	if get.ID != "getOrder" || get.Summary != "Get an order" || get.Path != "/orders/{id}" {
		t.Errorf("get = %+v", get)
	}
	// Path item parameters come first; the operation's replace them.
	if len(get.Parameters) != 2 || get.Parameters[0].Name != "id" || !get.Parameters[0].Required ||
		get.Parameters[1].Description != "Operation-level" {
		t.Errorf("parameters = %+v", get.Parameters)
	}
	var codes []string
	for _, r := range get.Responses {
		codes = append(codes, r.Code)
	}
	if !slices.Equal(codes, []string{"200", "404", "4XX", "default"}) {
		t.Errorf("codes = %q", codes)
	}
	ok := get.Responses[0]
	if len(ok.Headers) != 1 || ok.Headers[0].Name != "ETag" || ok.Headers[0].In != "header" {
		t.Errorf("headers = %+v", ok.Headers)
	}
	// The document's security applies; an operation can drop it.
	if len(get.Security) != 1 || get.Security[0][0].Scheme != "bearer" {
		t.Errorf("security = %+v", get.Security)
	}
	refund := ref.Groups[1].Operations[0]
	if refund.Security != nil {
		t.Errorf("refund security = %+v", refund.Security)
	}
	if len(ref.SecuritySchemes) != 1 || ref.SecuritySchemes[0].Scheme != "bearer" || ref.SecuritySchemes[0].BearerFormat != "JWT" {
		t.Errorf("schemes = %+v", ref.SecuritySchemes)
	}
}

func TestSchemasResolveReferencesAndStopAtCycles(t *testing.T) {
	ref := build(t, shop)
	order := ref.Groups[0].Operations[0].Responses[0].Content[0].Schema
	if order.Name != "Order" || order.Ref {
		t.Fatalf("order = %+v", order)
	}
	props := map[string]refdocs.Property{}
	for _, p := range order.Properties {
		props[p.Name] = p
	}
	if !props["id"].Required || !props["id"].Schema.ReadOnly || props["status"].Required {
		t.Errorf("required/readOnly: %+v", order.Properties)
	}
	if string(props["status"].Schema.Default) != `"open"` || len(props["status"].Schema.Enum) != 2 {
		t.Errorf("status = %+v", props["status"].Schema)
	}
	lines := props["lines"].Schema
	if !slices.Equal(lines.Constraints, []string{"at least 1 item"}) || lines.Items.Name != "Line" || lines.Items.Ref {
		t.Errorf("lines = %+v", lines)
	}
	sku := lines.Items.Properties[0].Schema
	if !slices.Equal(sku.Constraints, []string{"exactly 1 character", "matches ^[A-Z]+$"}) {
		t.Errorf("sku constraints = %q", sku.Constraints)
	}
	// Order refers to itself: the inner one is a link.
	if p := props["parent"].Schema; p.Name != "Order" || !p.Ref || len(p.Properties) != 0 {
		t.Errorf("parent = %+v", p)
	}
	if len(ref.Schemas) != 2 || ref.Schemas[0].Name != "Order" || ref.Schemas[0].Schema.Ref {
		t.Errorf("components = %+v", ref.Schemas)
	}

	body := ref.Groups[1].Operations[0].RequestBody
	amount, reason := body.Content[0].Schema.Properties[0].Schema, body.Content[0].Schema.Properties[1].Schema
	if !slices.Equal(amount.Constraints, []string{"> 0", "multiple of 0.01"}) {
		t.Errorf("amount = %q", amount.Constraints)
	}
	if !slices.Equal(reason.Types, []string{"string"}) || !reason.Nullable {
		t.Errorf("reason = %+v", reason)
	}
}

func TestExamples(t *testing.T) {
	ref := build(t, shop)
	body := ref.Groups[1].Operations[0].RequestBody
	if !body.Required || len(body.Content[0].Examples) != 2 {
		t.Fatalf("body = %+v", body)
	}
	full := body.Content[0].Examples[0]
	if full.Name != "full" || full.Summary != "The whole order" || !strings.Contains(full.Value, "25.5") {
		t.Errorf("full = %+v", full)
	}
	// Without examples, one is generated from the schema, and stays the same.
	order := ref.Groups[0].Operations[0].Responses[0].Content[0]
	if len(order.Examples) != 1 || !json.Valid([]byte(order.Examples[0].Value)) {
		t.Fatalf("generated = %+v", order.Examples)
	}
	if again := build(t, shop).Groups[0].Operations[0].Responses[0].Content[0].Examples[0]; again.Value != order.Examples[0].Value {
		t.Errorf("generated examples differ:\n%s\n%s", order.Examples[0].Value, again.Value)
	}
	// Text stays text.
	if ex := ref.Groups[2].Operations[0].Responses[0].Content[0].Examples; len(ex) != 1 || ex[0].Value != "ok" {
		t.Errorf("text = %+v", ex)
	}
}

func TestDeepGraphsStayBounded(t *testing.T) {
	// Each level refers to the next twice: fully expanded, the reference
	// would double in size at every level.
	var doc strings.Builder
	doc.WriteString("openapi: 3.1.1\ninfo: {title: Deep, version: '1'}\npaths:\n  /a:\n    get:\n      responses:\n        '200':\n          description: A.\n          content:\n            application/json:\n              schema: {$ref: '#/components/schemas/L0'}\ncomponents:\n  schemas:\n")
	for i := range 30 {
		next := "{type: string}"
		if i < 29 {
			next = "{$ref: '#/components/schemas/L" + strconv.Itoa(i+1) + "'}"
		}
		doc.WriteString("    L" + strconv.Itoa(i) + ":\n      type: object\n      properties:\n        left: " + next + "\n        right: " + next + "\n")
	}
	ref := build(t, doc.String())
	out, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 1<<20 {
		t.Errorf("reference is %d bytes", len(out))
	}
}

func TestSwagger2IsRefused(t *testing.T) {
	_, err := refdocs.Build([]byte(`{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{}}`))
	if !errors.Is(err, refdocs.ErrUnsupported) {
		t.Errorf("error = %v", err)
	}
}
