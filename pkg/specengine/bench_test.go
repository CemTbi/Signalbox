// SPDX-License-Identifier: Apache-2.0

package specengine_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

// largeSpec builds a valid OpenAPI 3.1 document with at least the given
// number of lines: CRUD operations and a schema per resource.
func largeSpec(minLines int) []byte {
	var b bytes.Buffer
	b.WriteString("openapi: 3.1.1\ninfo:\n  title: Large\n  version: 1.0.0\n  description: Generated for benchmarks.\n")
	b.WriteString("servers:\n  - url: https://api.example.com\ntags:\n  - name: items\n    description: Items.\npaths:\n")
	var schemas bytes.Buffer
	for i := 0; bytes.Count(b.Bytes(), []byte("\n"))+bytes.Count(schemas.Bytes(), []byte("\n")) < minLines; i++ {
		fmt.Fprintf(&b, `  /resources%[1]d:
    get:
      operationId: listResource%[1]d
      summary: List resource %[1]d
      description: Lists resource %[1]d.
      tags: [items]
      parameters:
        - name: limit
          in: query
          description: Page size.
          schema: {type: integer, minimum: 1, maximum: 100}
      responses:
        '200':
          description: A page.
          content:
            application/json:
              schema:
                type: array
                maxItems: 100
                items: {$ref: '#/components/schemas/Resource%[1]d'}
    post:
      operationId: createResource%[1]d
      summary: Create resource %[1]d
      description: Creates resource %[1]d.
      tags: [items]
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Resource%[1]d'}
      responses:
        '201':
          description: Created.
`, i)
		fmt.Fprintf(&schemas, `    Resource%[1]d:
      type: object
      description: Resource %[1]d.
      required: [id, name]
      properties:
        id: {type: string, format: uuid, description: ID.}
        name: {type: string, maxLength: 100, description: Name.}
        createdAt: {type: string, format: date-time, description: Creation time.}
`, i)
	}
	b.WriteString("components:\n  schemas:\n")
	b.Write(schemas.Bytes())
	return b.Bytes()
}

// TestLargeSpecMeetsBudget enforces the plan's target: lint results for a
// 10,000-line spec in under a second. The budget is doubled under the race
// detector and on slow CI machines via -short.
func TestLargeSpecMeetsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("performance budget check skipped in -short mode")
	}
	data := largeSpec(10_000)
	// Warm up the rule set cache and JSON schema compilation once.
	if _, err := specengine.Check(context.Background(), largeSpec(50), specengine.Options{}, specengine.RuleSetRecommended); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	rep, err := specengine.Check(context.Background(), data, specengine.Options{}, specengine.RuleSetRecommended)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Errors > 0 {
		t.Fatalf("generated spec is invalid: %+v", rep.Diagnostics[:min(5, len(rep.Diagnostics))])
	}
	t.Logf("checked %d lines in %v (%d diagnostics)", bytes.Count(data, []byte("\n")), elapsed, len(rep.Diagnostics))

	budget := time.Second
	if raceEnabled {
		budget *= 4
	}
	if elapsed > budget {
		t.Errorf("Check took %v for a 10,000-line spec, budget is %v", elapsed, budget)
	}
}

func BenchmarkCheck10kLines(b *testing.B) {
	data := largeSpec(10_000)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := specengine.Check(ctx, data, specengine.Options{}, specengine.RuleSetRecommended); err != nil {
			b.Fatal(err)
		}
	}
}
