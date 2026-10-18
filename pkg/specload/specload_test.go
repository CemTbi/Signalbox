// SPDX-License-Identifier: Apache-2.0

package specload_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/specload"
)

func TestLoadsOpenAPI3AndSwagger2(t *testing.T) {
	v3, err := specload.Load(specload.Document{Data: []byte("openapi: 3.1.1\ninfo: {title: A, version: '1'}\npaths: {/a: {get: {responses: {'200': {description: OK}}}}}\n"), Name: "a.yaml"}, specload.Options{})
	if err != nil || v3.Paths.Find("/a") == nil {
		t.Fatalf("OpenAPI 3.1: %v", err)
	}
	v2, err := specload.Load(specload.Document{Data: []byte(`{"swagger":"2.0","info":{"title":"B","version":"1"},"basePath":"/v1","paths":{"/b":{"get":{"responses":{"200":{"description":"OK"}}}}}}`), Name: "b.json"}, specload.Options{})
	if err != nil || v2.Paths.Find("/b") == nil {
		t.Fatalf("Swagger 2.0: %v %+v", err, v2)
	}
}

func TestMaxSchemas(t *testing.T) {
	var doc strings.Builder
	doc.WriteString("openapi: 3.1.1\ninfo: {title: Deep, version: '1'}\npaths:\n  /a:\n    get:\n      responses:\n        '200':\n          description: A.\n          content:\n            application/json:\n              schema: {$ref: '#/components/schemas/L0'}\ncomponents:\n  schemas:\n")
	for i := range 20 {
		next := "{type: string}"
		if i < 19 {
			next = fmt.Sprintf("{$ref: '#/components/schemas/L%d'}", i+1)
		}
		fmt.Fprintf(&doc, "    L%d:\n      type: object\n      properties:\n        left: %s\n        right: %s\n", i, next, next)
	}
	d := specload.Document{Data: []byte(doc.String()), Name: "deep.yaml"}
	// Without a limit nothing is measured.
	if _, err := specload.Load(d, specload.Options{}); err != nil {
		t.Errorf("no limit: %v", err)
	}
	if _, err := specload.Load(d, specload.Options{MaxSchemas: 1000}); !errors.Is(err, specload.ErrTooLarge) {
		t.Errorf("limit: %v", err)
	}
}
