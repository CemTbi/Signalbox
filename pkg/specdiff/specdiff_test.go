// SPDX-License-Identifier: Apache-2.0

package specdiff_test

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/pkg/specdiff"
)

const base = `openapi: 3.1.1
info: {title: Orders, version: 1.0.0}
paths:
  /orders:
    get:
      operationId: listOrders
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
  /legacy:
    get:
      responses: {'200': {description: OK}}
components:
  schemas:
    Order:
      type: object
      required: [id]
      properties:
        id: {type: integer}
        note: {type: [string, 'null']}
`

const head = `openapi: 3.1.1
info: {title: Orders, version: 1.1.0}
paths:
  /orders:
    get:
      operationId: listOrders
      parameters:
        - {name: limit, in: query, required: true, schema: {type: integer}}
        - {name: since, in: query, schema: {type: string}}
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
  /refunds:
    post:
      responses: {'201': {description: OK}}
components:
  schemas:
    Order:
      type: object
      required: [id]
      properties:
        id: {type: string}
        total: {type: number}
`

func compare(t *testing.T, a, b string) *specdiff.Report {
	t.Helper()
	r, err := specdiff.Compare(specdiff.Document{Data: []byte(a), Name: "base.yaml"}, specdiff.Document{Data: []byte(b), Name: "head.yaml"}, specdiff.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func find(r *specdiff.Report, id string) *specdiff.Change {
	for i := range r.Changes {
		if r.Changes[i].ID == id {
			return &r.Changes[i]
		}
	}
	return nil
}

func TestBreakingChangesNameTheOperationAndLines(t *testing.T) {
	r := compare(t, base, head)
	required := find(r, "request-parameter-became-required")
	if required == nil || required.Level != specdiff.LevelError || required.Method != http.MethodGet || required.Path != "/orders" ||
		required.OperationID != "listOrders" || !strings.Contains(required.Message, "limit") {
		t.Fatalf("became required = %+v", required)
	}
	if required.Head == nil || required.Head.Line != 8 {
		t.Errorf("became required is at %+v, want head line 8", required.Head)
	}
	// A removed path is only in the base.
	removed := find(r, "api-path-removed-without-deprecation")
	if removed == nil || removed.Level != specdiff.LevelError || removed.Base == nil || removed.Base.Line != 16 || removed.Head != nil {
		t.Errorf("path removed = %+v", removed)
	}
	if c := find(r, "response-property-type-changed"); c == nil || c.Level != specdiff.LevelError {
		t.Errorf("type changed = %+v", c)
	}
	// Additions don't break clients.
	if c := find(r, "endpoint-added"); c == nil || c.Level != specdiff.LevelInfo {
		t.Errorf("endpoint added = %+v", c)
	}
	if c := find(r, "new-optional-request-parameter"); c == nil || c.Level != specdiff.LevelInfo {
		t.Errorf("optional parameter = %+v", c)
	}
	if r.Counts.Errors != 3 || r.Counts.Errors+r.Counts.Warnings+r.Counts.Infos != len(r.Changes) {
		t.Errorf("counts = %+v for %d changes", r.Counts, len(r.Changes))
	}
}

func TestTheSameDocumentHasNoChanges(t *testing.T) {
	if r := compare(t, base, base); len(r.Changes) != 0 {
		t.Errorf("changes = %+v", r.Changes)
	}
}

func TestSwagger2IsComparedAsOpenAPI3(t *testing.T) {
	v2 := `swagger: '2.0'
info: {title: Legacy, version: '1.0'}
paths:
  /ping:
    get:
      responses: {'200': {description: Pong.}}
  /pong:
    get:
      responses: {'200': {description: Ping.}}
`
	r := compare(t, v2, strings.Replace(v2, "  /pong:\n    get:\n      responses: {'200': {description: Ping.}}\n", "", 1))
	if c := find(r, "api-path-removed-without-deprecation"); c == nil || c.Path != "/pong" {
		t.Errorf("changes = %+v", r.Changes)
	}
}

func TestFileReferencesNeedADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order.yaml"), []byte("type: object\nproperties:\n  id: {type: integer}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := `openapi: 3.1.1
info: {title: Orders, version: 1.0.0}
paths:
  /orders:
    get:
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {$ref: 'order.yaml'}
`
	a := specdiff.Document{Data: []byte(doc), Name: "openapi.yaml", Dir: dir}
	if _, err := specdiff.Compare(a, a, specdiff.Options{}); err != nil {
		t.Errorf("with a directory: %v", err)
	}
	a.Dir = ""
	if _, err := specdiff.Compare(a, a, specdiff.Options{}); err == nil {
		t.Error("followed a file reference without a directory")
	}
	remote := specdiff.Document{Data: []byte(strings.Replace(doc, "'order.yaml'", "'https://example.com/order.yaml'", 1)), Name: "openapi.yaml", Dir: dir}
	if _, err := specdiff.Compare(remote, remote, specdiff.Options{}); err == nil {
		t.Error("followed a remote reference without permission")
	}
}

// Comparing walks every schema with references expanded, so a schema
// shared many times over is refused at once.
func TestDocumentsTooLargeToCompare(t *testing.T) {
	var doc strings.Builder
	doc.WriteString("openapi: 3.1.1\ninfo: {title: Deep, version: '1'}\npaths:\n  /a:\n    get:\n      responses:\n        '200':\n          description: A.\n          content:\n            application/json:\n              schema: {$ref: '#/components/schemas/L0'}\ncomponents:\n  schemas:\n")
	for i := range 30 {
		next := "{type: string}"
		if i < 29 {
			next = fmt.Sprintf("{$ref: '#/components/schemas/L%d'}", i+1)
		}
		fmt.Fprintf(&doc, "    L%d:\n      type: object\n      properties:\n        left: %s\n        right: %s\n", i, next, next)
	}
	start := time.Now()
	d := specdiff.Document{Data: []byte(doc.String()), Name: "deep.yaml"}
	if _, err := specdiff.Compare(d, d, specdiff.Options{}); !errors.Is(err, specdiff.ErrTooLarge) {
		t.Errorf("error = %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("refusing took %v", took)
	}
}

func TestParseLevel(t *testing.T) {
	if l, err := specdiff.ParseLevel(" Warning "); err != nil || l != specdiff.LevelWarning || !l.AtLeast(specdiff.LevelInfo) || l.AtLeast(specdiff.LevelError) {
		t.Errorf("ParseLevel = %q, %v", l, err)
	}
	if _, err := specdiff.ParseLevel("fatal"); err == nil {
		t.Error("accepted fatal")
	}
}
