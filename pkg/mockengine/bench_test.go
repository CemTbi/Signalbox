// SPDX-License-Identifier: Apache-2.0

package mockengine_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkGenerated measures a GET answered with data generated from the
// schema, the slowest common case.
func BenchmarkGenerated(b *testing.B) {
	m := mock(&testing.T{})
	for b.Loop() {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/orders?limit=5", nil))
		if w.Code != http.StatusOK {
			b.Fatal(w.Code)
		}
	}
}

// BenchmarkValidatedPost measures a POST whose body is checked against the
// schema and answered from an example.
func BenchmarkValidatedPost(b *testing.B) {
	m := mock(&testing.T{})
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"A-1","qty":2}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			b.Fatal(w.Code)
		}
	}
}
