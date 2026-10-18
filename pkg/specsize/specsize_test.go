// SPDX-License-Identifier: Apache-2.0

package specsize_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pb33f/libopenapi"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/CemTbi/Signalbox/pkg/specsize"
)

func model(t *testing.T, doc string) *v3.Document {
	t.Helper()
	d, err := libopenapi.NewDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.BuildV3Model()
	if err != nil {
		t.Fatal(err)
	}
	return &m.Model
}

// doubling refers to the next level twice at each of levels levels.
func doubling(levels int) string {
	var doc strings.Builder
	doc.WriteString(`openapi: 3.1.1
info: {title: Deep, version: '1'}
paths:
  /a:
    get:
      parameters:
        - {name: q, in: query, schema: {type: string}}
      responses:
        '200':
          description: A.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/L0'}
components:
  schemas:
`)
	for i := range levels {
		next := "{type: string}"
		if i < levels-1 {
			next = fmt.Sprintf("{$ref: '#/components/schemas/L%d'}", i+1)
		}
		fmt.Fprintf(&doc, `    L%d:
      type: object
      properties:
        left: %s
        right: %s
`, i, next, next)
	}
	return doc.String()
}

func TestCountsExpandedSchemas(t *testing.T) {
	// Level i expands to 2^(levels-i+1) - 1 schemas; the parameter adds one.
	m := model(t, doubling(4))
	if n := specsize.Operations(m, 1000); n != 31+1 {
		t.Errorf("Operations = %d, want 32", n)
	}
	l0 := m.Components.Schemas.GetOrZero("L0")
	if n := specsize.Schema(l0, 1000); n != 31 {
		t.Errorf("Schema(L0) = %d, want 31", n)
	}
}

func TestStopsAtTheLimitQuickly(t *testing.T) {
	m := model(t, doubling(40))
	start := time.Now()
	n := specsize.Operations(m, 10_000)
	if n <= 10_000 {
		t.Errorf("Operations = %d, want more than the limit", n)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("counting took %v", d)
	}
}

func TestCyclesCountOnce(t *testing.T) {
	m := model(t, `openapi: 3.1.1
info: {title: Tree, version: '1'}
paths:
  /tree:
    get:
      responses:
        '200':
          description: The tree.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Node'}
components:
  schemas:
    Node:
      type: object
      properties:
        children: {type: array, items: {$ref: '#/components/schemas/Node'}}
`)
	// Node, children, and the reference back to Node.
	if n := specsize.Operations(m, 1000); n != 3 {
		t.Errorf("Operations = %d, want 3", n)
	}
}
