// SPDX-License-Identifier: Apache-2.0

package contract_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/contract"
)

const orders = `openapi: 3.1.0
info: {title: Orders, version: 1.0.0}
paths:
  /orders/{orderId}:
    get:
      parameters:
        - {name: orderId, in: path, required: true, schema: {type: integer}}
      responses:
        '200':
          description: The order.
          content:
            application/json:
              schema:
                type: object
                required: [id, status]
                properties:
                  id: {type: integer}
                  status: {type: string, enum: [open, paid]}
        4XX:
          description: A problem.
  /health:
    get:
      responses:
        default: {description: Anything.}
`

func check(t *testing.T, c *contract.Checker, op contract.Operation, status int, body string) []contract.Result {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://api.test"+strings.ReplaceAll(op.Path, "{orderId}", "42"), nil)
	resp := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
	return c.Check(op, req, resp)
}

func TestCheck(t *testing.T) {
	c, err := contract.NewChecker([]byte(orders))
	if err != nil {
		t.Fatal(err)
	}
	get := contract.Operation{Method: "GET", Path: "/orders/{orderId}"}
	for _, tt := range []struct {
		name   string
		op     contract.Operation
		status int
		body   string
		passed []bool
		error  string
	}{
		{"matches", get, 200, `{"id": 42, "status": "paid"}`, []bool{true, true}, ""},
		{"wrong body", get, 200, `{"id": "42"}`, []bool{true, false}, "status"},
		{"a range", get, 404, `{}`, []bool{true, true}, ""},
		{"undocumented status", get, 500, ``, []bool{false}, "It documents 200, 4XX."},
		{"a default response", contract.Operation{Method: "GET", Path: "/health"}, 503, `{}`, []bool{true, true}, ""},
		{"gone from the spec", contract.Operation{Method: "DELETE", Path: "/orders/{orderId}"}, 204, ``, []bool{false}, "no longer has DELETE /orders/{orderId}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := check(t, c, tt.op, tt.status, tt.body)
			if len(got) != len(tt.passed) {
				t.Fatalf("results = %+v", got)
			}
			for i, r := range got {
				if r.Passed != tt.passed[i] {
					t.Errorf("result %d = %+v", i, r)
				}
			}
			if tt.error != "" && !strings.Contains(got[len(got)-1].Error, tt.error) {
				t.Errorf("error = %q, want %q", got[len(got)-1].Error, tt.error)
			}
		})
	}
}

func TestNewChecker(t *testing.T) {
	if _, err := contract.NewChecker([]byte("swagger: '2.0'\ninfo: {title: x, version: '1'}\npaths: {}\n")); !errors.Is(err, contract.ErrUnsupported) {
		t.Errorf("swagger 2: %v", err)
	}
	if _, err := contract.NewChecker([]byte("{")); err == nil {
		t.Error("an unreadable document made a checker")
	}
}
