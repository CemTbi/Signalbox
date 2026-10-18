// SPDX-License-Identifier: Apache-2.0

package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/codegen"
)

const shop = `openapi: 3.1.1
info: {title: Shop API, version: 2.0.0}
paths:
  /orders/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: integer}}
    get:
      operationId: getOrder
      summary: Get an order
      parameters:
        - {name: expand, in: query, schema: {type: array, items: {type: string, enum: [lines, customer]}}}
        - {name: X-Trace-Id, in: header, schema: {type: string}}
        - {name: session, in: cookie, schema: {type: string}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
        '404':
          description: No such order.
          content:
            application/problem+json:
              schema: {type: object, properties: {title: {type: string}, detail: {type: string}}}
    delete:
      operationId: delete_order
      deprecated: true
      responses:
        '204': {description: Deleted.}
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/order.Draft'}
      responses:
        '201':
          description: Created.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
  /health:
    get:
      operationId: health
      responses:
        '200':
          description: Up.
          content:
            text/plain:
              schema: {type: string}
components:
  schemas:
    Order:
      description: An order. */ is escaped.
      type: object
      required: [id, status, lines]
      properties:
        id: {type: integer}
        status: {type: string, enum: [open, paid]}
        lines: {type: array, items: {$ref: '#/components/schemas/Line'}}
        note: {type: [string, 'null']}
        reference: {type: [string, integer]}
        kind: {const: order}
        meta: {type: object, additionalProperties: {type: string}}
        total-due: {type: number}
    Line:
      allOf:
        - {$ref: '#/components/schemas/order.Draft'}
        - {type: object, properties: {qty: {type: integer}}}
    order.Draft:
      type: object
      required: [sku]
      properties:
        sku: {type: string}
`

func tsSource(t *testing.T, doc string) string {
	t.Helper()
	r := generate(t, doc, codegen.TargetTypeScriptClient)
	if len(r.Files) != 2 || r.Files[0].Path != r.Package+"/index.ts" {
		t.Fatalf("files = %+v", r.Files)
	}
	return string(r.Files[0].Content)
}

func TestTypeScriptTypesAndMethods(t *testing.T) {
	src := tsSource(t, shop)
	for _, want := range []string{
		"export type Order = {\n",
		`  status: "open" | "paid";`,
		"  lines: Line[];",
		"  note?: string | null;",
		"  reference?: string | number;",
		`  kind?: "order";`,
		"  meta?: { [key: string]: string };",
		`  "total-due"?: number;`,
		"export type Line = OrderDraft & {\n",
		"export type OrderDraft = {\n",
		"An order. *\\/ is escaped.",
		"    getOrder(params: {\n",
		`      expand?: ("lines" | "customer")[];`,
		`      "X-Trace-Id"?: string;`,
		"    }, options?: RequestOptions): Promise<Order> {",
		`        path: { id: params.id },`,
		`        query: [["expand", params.expand, "form"]],`,
		`        headers: [["X-Trace-Id", params["X-Trace-Id"]]],`,
		"    deleteOrder(params: {\n",
		"@deprecated",
		"): Promise<void> {",
		"    postOrders(params: {\n      body: OrderDraft;\n",
		"    health(options?: RequestOptions): Promise<string> {",
		"export function createClient(options: ClientOptions) {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("client doesn't contain %q", want)
		}
	}
	// Browsers don't let scripts set cookies.
	if strings.Contains(src, "session") {
		t.Error("the client has the cookie parameter")
	}
	// The same document gives the same code.
	if again := tsSource(t, shop); again != src {
		t.Error("generating twice gave different code")
	}
}

// The client type-checks under strict TypeScript and works against a fake
// fetch. This needs npx and downloads TypeScript, so -short skips it.
func TestTypeScriptClientCompilesAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads TypeScript")
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		t.Skip("npx isn't installed")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("index.ts", tsSource(t, shop))
	// Signalbox's own API is a large, real document.
	own, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	write("signalbox.ts", tsSource(t, string(own)))
	write("package.json", `{"type": "module"}`)
	write("check.ts", checkScript)
	tsc := exec.Command(npx, "--yes", "-p", "typescript@5.9.3", "tsc", "--strict", "--noEmitOnError", "--noUnusedLocals",
		"--target", "es2022", "--module", "nodenext", "--moduleResolution", "nodenext", "--lib", "es2022,dom", "--outDir", "out", "index.ts", "signalbox.ts", "check.ts")
	tsc.Dir = dir
	if out, err := tsc.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %v\n%s", err, out)
	}
	node := exec.Command("node", filepath.Join("out", "check.js"))
	node.Dir = dir
	if out, err := node.CombinedOutput(); err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("node: %v\n%s", err, out)
	}
}

// checkScript calls the client with a fake fetch and checks what it sent
// and what it returned.
const checkScript = `import { ApiError, createClient } from "./index.js";

const sent: { url: string; init: RequestInit }[] = [];
function reply(status: number, body: string, type: string) {
  return async (url: string, init: RequestInit) => {
    sent.push({ url, init });
    return new Response(status === 204 ? null : body, { status, headers: { "Content-Type": type } });
  };
}
function check(ok: boolean, what: string) {
  if (!ok) throw new Error("failed: " + what);
}

const order = '{"id":7,"status":"open","lines":[]}';
let api = createClient({ baseUrl: "https://mock.test/v1/", headers: () => ({ Authorization: "Bearer t" }), fetch: reply(200, order, "application/json") });
const got = await api.getOrder({ id: 7, expand: ["lines", "customer"], "X-Trace-Id": "abc" });
check(got.id === 7 && got.status === "open", "decodes the order");
check(sent[0].url === "https://mock.test/v1/orders/7?expand=lines&expand=customer", "builds the URL: " + sent[0].url);
const h = new Headers(sent[0].init.headers);
check(h.get("Authorization") === "Bearer t" && h.get("X-Trace-Id") === "abc" && h.get("Accept") === "application/json", "sends the headers");
check(sent[0].init.method === "GET", "uses GET");

api = createClient({ baseUrl: "https://mock.test", fetch: reply(201, order, "application/json") });
await api.postOrders({ body: { sku: "A-1" } }, { headers: { "Idempotency-Key": "k" } });
check(sent[1].init.body === '{"sku":"A-1"}' && new Headers(sent[1].init.headers).get("Content-Type") === "application/json", "sends JSON");
check(new Headers(sent[1].init.headers).get("Idempotency-Key") === "k", "adds the request's headers");

api = createClient({ baseUrl: "https://mock.test", fetch: reply(204, "", "") });
check((await api.deleteOrder({ id: 7 })) === undefined, "204 is void");

api = createClient({ baseUrl: "https://mock.test", fetch: reply(200, "ok", "text/plain") });
check((await api.health()) === "ok", "reads text");

api = createClient({ baseUrl: "https://mock.test", fetch: reply(404, '{"title":"Not Found","detail":"No order 9."}', "application/problem+json") });
try {
  await api.getOrder({ id: 9 });
  check(false, "throws on 404");
} catch (e) {
  check(e instanceof ApiError && e.status === 404 && e.message === "404 Not Found: No order 9.", "ApiError: " + String(e));
}
console.log("ok");
`

func TestTypeScriptNamesStayUnique(t *testing.T) {
	src := tsSource(t, `openapi: 3.1.1
info: {title: Names, version: '1'}
paths:
  /a:
    get: {operationId: list, responses: {'200': {description: OK}}}
  /b:
    get: {operationId: list, responses: {'200': {description: OK}}}
  /c/{id}:
    get: {responses: {'200': {description: OK}}, parameters: [{name: id, in: path, required: true, schema: {type: string}}]}
components:
  schemas:
    order: {type: string}
    Order: {type: integer}
    ApiError: {type: string}
`)
	for _, want := range []string{"    list(", "    list2(", "    getCById(", "export type Order = number;", "export type Order2 = string;", "export type ApiError2 = string;"} {
		if !strings.Contains(src, want) {
			t.Errorf("client doesn't contain %q", want)
		}
	}
}
