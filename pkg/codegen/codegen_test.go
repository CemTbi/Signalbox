// SPDX-License-Identifier: Apache-2.0

package codegen_test

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/codegen"
	"github.com/CemTbi/Signalbox/pkg/specload"
)

const orders = `openapi: 3.1.1
info: {title: Orders API, version: 1.0.0}
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer, format: int64}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
        '404': {description: No such order.}
  /orders:
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Order'}
      responses:
        '201': {description: Created.}
components:
  schemas:
    Order:
      type: object
      required: [id, status]
      properties:
        id: {type: integer, format: int64}
        status: {type: string, enum: [open, paid]}
        kind: {const: order}
        reference: {type: [string, integer]}
        note: {type: [string, 'null']}
`

func generate(t *testing.T, doc string, target codegen.Target) *codegen.Result {
	t.Helper()
	r, err := codegen.Generate(specload.Document{Data: []byte(doc), Name: "openapi.yaml"}, codegen.Options{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func source(t *testing.T, r *codegen.Result) string {
	t.Helper()
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, ".gen.go") {
			if _, err := parser.ParseFile(token.NewFileSet(), f.Path, f.Content, 0); err != nil {
				t.Fatalf("%s doesn't parse: %v", f.Path, err)
			}
			return string(f.Content)
		}
	}
	t.Fatal("no Go file generated")
	return ""
}

func TestGoClient(t *testing.T) {
	r := generate(t, orders, codegen.TargetGoClient)
	if r.Package != "orders" || len(r.Files) != 2 || r.Files[0].Path != "orders/orders.gen.go" || r.Files[1].Path != "orders/README.md" {
		t.Fatalf("result = %+v", r)
	}
	code := source(t, r)
	for _, want := range []string{
		"package orders",
		"func (c *ClientWithResponses) GetOrderWithResponse(ctx context.Context, id int64",
		"CreateOrder(ctx context.Context, body CreateOrderJSONRequestBody",
		// const became a one-value enum, and several types a union.
		"OrderKindOrder OrderKind = \"order\"",
		"func (t Order_Reference) AsOrderReference0() (OrderReference0, error)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("client doesn't contain %q", want)
		}
	}
	// A type with null is a pointer.
	if !regexp.MustCompile(`Note\s+\*string`).MatchString(code) {
		t.Error("note isn't a *string")
	}
	if strings.Contains(code, "StrictServerInterface") {
		t.Error("the client has a server")
	}
}

func TestGoServer(t *testing.T) {
	code := source(t, generate(t, orders, codegen.TargetGoServer))
	for _, want := range []string{
		"type StrictServerInterface interface",
		"GetOrder(ctx context.Context, request GetOrderRequestObject) (GetOrderResponseObject, error)",
		"func HandlerFromMux(si ServerInterface, m ServeMux) http.Handler",
		"func GetSwagger() (*openapi3.T, error)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("server doesn't contain %q", want)
		}
	}
}

func TestWarningsNameWhatIsLost(t *testing.T) {
	doc := `openapi: 3.2.0
info: {title: Things, version: 1.0.0}
paths:
  /things:
    query:
      responses: {'200': {description: OK}}
    get:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  pair: {type: array, prefixItems: [{type: string}, {type: integer}]}
                  labels: {type: object, patternProperties: {'^x-': {type: string}}}
              examples:
                odd: {value: {schema: {prefixItems: [1]}}}
`
	r := generate(t, doc, codegen.TargetGoClient)
	var got []string
	for _, w := range r.Warnings {
		got = append(got, w.Message)
	}
	if len(r.Warnings) != 3 {
		t.Fatalf("warnings = %q", got)
	}
	if w := r.Warnings[0]; w.Line != 5 || !strings.Contains(w.Message, "QUERY") {
		t.Errorf("first = %+v", w)
	}
	if w := r.Warnings[1]; w.Line != 16 || !strings.Contains(w.Message, "prefixItems") {
		t.Errorf("second = %+v", w)
	}
	if w := r.Warnings[2]; w.Line != 17 || !strings.Contains(w.Message, "patternProperties") {
		t.Errorf("third = %+v", w)
	}
}

func TestPackageNames(t *testing.T) {
	for title, want := range map[string]string{"Orders API": "orders", "Payments v2": "paymentsv2", "3D Printing Service": "dprinting", "API": "api", "Café Menu": "cafmenu"} {
		doc := strings.Replace(orders, "Orders API", title, 1)
		if r := generate(t, doc, codegen.TargetGoClient); r.Package != want {
			t.Errorf("%q: package %q, want %q", title, r.Package, want)
		}
	}
	_, err := codegen.Generate(specload.Document{Data: []byte(orders), Name: "o.yaml"}, codegen.Options{Target: codegen.TargetGoClient, Package: "Orders"})
	if !errors.Is(err, codegen.ErrInvalidPackage) {
		t.Errorf("package Orders: %v", err)
	}
	if _, err := codegen.ParseTarget("rust-client"); err == nil {
		t.Error("parsed rust-client")
	}
}

func TestSwagger2(t *testing.T) {
	code := source(t, generate(t, `swagger: '2.0'
info: {title: Legacy, version: '1.0'}
paths:
  /ping:
    get:
      operationId: ping
      responses: {'200': {description: Pong.}}
`, codegen.TargetGoClient))
	if !strings.Contains(code, "Ping(ctx context.Context") {
		t.Error("no Ping method")
	}
}

// The generated code compiles against oapi-codegen's runtime. This
// downloads modules, so -short skips it.
func TestGeneratedCodeBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads modules")
	}
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/shop\n\ngo 1.25\n")
	for _, target := range []codegen.Target{codegen.TargetGoClient, codegen.TargetGoServer} {
		r, err := codegen.Generate(specload.Document{Data: []byte(orders), Name: "openapi.yaml"},
			codegen.Options{Target: target, Package: strings.ReplaceAll(string(target), "-", "")})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range r.Files {
			write(f.Path, string(f.Content))
		}
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"vet", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
