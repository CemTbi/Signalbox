// SPDX-License-Identifier: Apache-2.0

package mockengine_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/mockengine"
)

const orders = `openapi: 3.1.1
info:
  title: Orders API
  version: 1.0.0
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    get:
      operationId: listOrders
      parameters:
        - name: limit
          in: query
          schema: {type: integer, minimum: 1, maximum: 100}
      responses:
        '200':
          description: Orders.
          content:
            application/json:
              schema:
                type: array
                items: {$ref: '#/components/schemas/Order'}
    post:
      operationId: createOrder
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [sku, qty]
              properties:
                sku: {type: string}
                qty: {type: integer, minimum: 1}
      responses:
        '201':
          description: Created.
          content:
            application/json:
              examples:
                small:
                  value: {id: 1, sku: A-1, qty: 1}
                large:
                  value: {id: 2, sku: A-1, qty: 500}
        '400':
          description: Invalid.
          content:
            application/problem+json:
              example: {title: Bad Request, status: 400}
  /orders/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: integer}}
    get:
      operationId: getOrder
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
            application/yaml:
              schema: {$ref: '#/components/schemas/Order'}
        4XX:
          description: Client errors.
          content:
            application/json:
              example: {error: not found}
    delete:
      operationId: deleteOrder
      responses:
        '204':
          description: Deleted.
  /health:
    get:
      responses:
        '200':
          description: Up.
          content:
            text/plain:
              example: ok
components:
  schemas:
    Order:
      type: object
      required: [id, sku, qty, status]
      properties:
        id: {type: integer}
        sku: {type: string}
        qty: {type: integer, minimum: 1}
        status: {type: string, enum: [open, paid, shipped]}
`

func mock(t *testing.T) *mockengine.Mock {
	t.Helper()
	m, err := mockengine.New([]byte(orders))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func do(t *testing.T, m *mockengine.Mock, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	return w
}

type problemDoc struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Errors []struct {
		Location, Detail string
	} `json:"errors"`
}

func readProblem(t *testing.T, w *httptest.ResponseRecorder) problemDoc {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, body %s", ct, w.Body)
	}
	var p problemDoc
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGeneratedBodiesFollowTheSchemaAndStayTheSame(t *testing.T) {
	m := mock(t)
	w := do(t, m, http.MethodGet, "/orders/42", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET /orders/42 = %d %s", w.Code, w.Body)
	}
	var order struct {
		ID     *int    `json:"id"`
		SKU    *string `json:"sku"`
		Qty    *int    `json:"qty"`
		Status string  `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &order); err != nil {
		t.Fatal(err)
	}
	if order.ID == nil || order.SKU == nil || order.Qty == nil || *order.Qty < 1 ||
		(order.Status != "open" && order.Status != "paid" && order.Status != "shipped") {
		t.Errorf("order = %s", w.Body)
	}
	again := do(t, m, http.MethodGet, "/orders/42", "")
	if again.Body.String() != w.Body.String() {
		t.Errorf("the same request got different data:\n%s\n%s", w.Body, again.Body)
	}
	// Arrays of objects.
	list := do(t, m, http.MethodGet, "/orders?limit=5", "")
	var items []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &items); err != nil || list.Code != http.StatusOK {
		t.Errorf("GET /orders = %d %s", list.Code, list.Body)
	}
}

func TestExamplesAndPreferences(t *testing.T) {
	m := mock(t)
	order := `{"sku":"A-1","qty":2}`
	w := do(t, m, http.MethodPost, "/orders", order)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"qty": 1`) {
		t.Errorf("first example: %d %s", w.Code, w.Body)
	}
	w = do(t, m, http.MethodPost, "/orders", order, "Prefer", "example=large")
	if !strings.Contains(w.Body.String(), `"qty": 500`) || w.Header().Get("Preference-Applied") != "example=large" {
		t.Errorf("named example: %s, applied %q", w.Body, w.Header().Get("Preference-Applied"))
	}
	w = do(t, m, http.MethodPost, "/orders", `{}`, "Prefer", "code=400")
	if w.Code != http.StatusBadRequest || w.Header().Get("Content-Type") != "application/problem+json" || w.Header().Get("Preference-Applied") != "code=400" {
		t.Errorf("Prefer code=400: %d %s %v", w.Code, w.Body, w.Header())
	}
	// A code covered by a range.
	if w := do(t, m, http.MethodGet, "/orders/1", "", "Prefer", "code=404"); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("Prefer code=404 through 4XX: %d %s", w.Code, w.Body)
	}
	// No content.
	if w := do(t, m, http.MethodDelete, "/orders/1", ""); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Errorf("DELETE = %d %q", w.Code, w.Body)
	}
	// Text stays text.
	if w := do(t, m, http.MethodGet, "/health", ""); w.Body.String() != "ok" || w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("GET /health = %q %q", w.Body, w.Header().Get("Content-Type"))
	}
	// Accept picks the media type.
	if w := do(t, m, http.MethodGet, "/orders/1", "", "Accept", "application/yaml"); w.Header().Get("Content-Type") != "application/yaml" || !strings.Contains(w.Body.String(), "sku:") {
		t.Errorf("YAML: %q %s", w.Header().Get("Content-Type"), w.Body)
	}
}

func TestInvalidRequestsNameWhatIsWrong(t *testing.T) {
	m := mock(t)
	for _, tt := range []struct {
		method, path, body, where string
	}{
		{http.MethodGet, "/orders/abc", "", "path parameter id"},
		{http.MethodGet, "/orders?limit=500", "", "query parameter limit"},
		{http.MethodPost, "/orders", `{"sku":"A-1","qty":0}`, "/qty"},
		{http.MethodPost, "/orders", `{"qty":1}`, ""},
	} {
		w := do(t, m, tt.method, tt.path, tt.body)
		p := readProblem(t, w)
		if w.Code != http.StatusBadRequest || p.Code != "invalid-request" || len(p.Errors) == 0 {
			t.Errorf("%s %s: %d %+v", tt.method, tt.path, w.Code, p)
			continue
		}
		if tt.where != "" && !strings.Contains(w.Body.String(), tt.where) {
			t.Errorf("%s %s: errors don't name %q: %s", tt.method, tt.path, tt.where, w.Body)
		}
	}
}

func TestUnknownPathsAndMethods(t *testing.T) {
	m := mock(t)
	w := do(t, m, http.MethodGet, "/nope", "")
	if p := readProblem(t, w); w.Code != http.StatusNotFound || p.Code != "no-such-path" {
		t.Errorf("unknown path: %d %+v", w.Code, p)
	}
	w = do(t, m, http.MethodPut, "/orders", `{}`)
	if p := readProblem(t, w); w.Code != http.StatusMethodNotAllowed || p.Code != "method-not-allowed" || !strings.Contains(w.Header().Get("Allow"), "POST") {
		t.Errorf("wrong method: %d %+v Allow %q", w.Code, p, w.Header().Get("Allow"))
	}
}

func TestServerBasePath(t *testing.T) {
	m := mock(t)
	// The document's server URL ends in /v1; requests may include it.
	if w := do(t, m, http.MethodGet, "/v1/orders/7", ""); w.Code != http.StatusOK {
		t.Errorf("GET /v1/orders/7 = %d %s", w.Code, w.Body)
	}
}

func TestSwagger2IsRefused(t *testing.T) {
	_, err := mockengine.New([]byte(`{"swagger":"2.0","info":{"title":"x","version":"1"},"paths":{}}`))
	if !errors.Is(err, mockengine.ErrUnsupported) {
		t.Errorf("error = %v", err)
	}
}
