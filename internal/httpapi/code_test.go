// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestGeneratedCodeDownloads(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Orders", "slug": "orders"}}).decode(t, &svc)
	var spec httpapi.SpecJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": "Orders API", "content": ordersSpec}}).decode(t, &spec)
	var v httpapi.VersionJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}}).decode(t, &v)
	codePath := "/api/v1/versions/" + v.ID + "/code"

	r := h.do(call{method: http.MethodGet, path: codePath + "?target=typescript-client"})
	r.expect(t, http.StatusOK, "")
	if r.header.Get("Content-Type") != "application/zip" ||
		r.header.Get("Content-Disposition") != `attachment; filename="orders-typescript-client-1.0.0.zip"` {
		t.Errorf("headers = %v", r.header)
	}
	files := unzip(t, r.body)
	if !strings.Contains(files["orders/index.ts"], "export function createClient") || files["orders/README.md"] == "" {
		t.Errorf("archive has %d files: %v", len(files), keys(files))
	}
	// The same request gives the same archive, and its ETag gets 304.
	again := h.do(call{method: http.MethodGet, path: codePath + "?target=typescript-client"})
	if !bytes.Equal(again.body, r.body) {
		t.Error("the archive changed between downloads")
	}
	h.do(call{method: http.MethodGet, path: codePath + "?target=typescript-client", headers: map[string]string{"If-None-Match": r.header.Get("ETag")}}).
		expect(t, http.StatusNotModified, "")

	// Go, with a package name.
	r = h.do(call{method: http.MethodGet, path: codePath + "?target=go-server&package=shop"})
	r.expect(t, http.StatusOK, "")
	if !strings.Contains(unzip(t, r.body)["shop/shop.gen.go"], "package shop") {
		t.Error("no shop package in the Go archive")
	}

	h.do(call{method: http.MethodGet, path: codePath + "?target=cobol", invalid: true}).expect(t, http.StatusBadRequest, "invalid-target")
	h.do(call{method: http.MethodGet, path: codePath + "?target=go-client&package=func", invalid: true}).expect(t, http.StatusBadRequest, "invalid-package")

	h.createUser("Mallory", "mallory@example.com")
	h.signOutLocally()
	h.signIn("mallory@example.com")
	h.do(call{method: http.MethodGet, path: codePath + "?target=go-client"}).expect(t, http.StatusNotFound, "not-found")
}

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(b)
	}
	return files
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// What the code can't express is listed in the archive, and a document the
// generator can't read is refused with its reason.
func TestGeneratedCodeWarningsAndFailures(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var svc httpapi.ServiceJSON
	h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/services", body: map[string]string{"name": "Pairs", "slug": "pairs"}}).decode(t, &svc)
	publish := func(name, content string) string {
		t.Helper()
		var spec httpapi.SpecJSON
		h.do(call{method: http.MethodPost, path: "/api/v1/services/" + svc.ID + "/specs", body: map[string]string{"name": name, "content": content}}).decode(t, &spec)
		var v httpapi.VersionJSON
		r := h.do(call{method: http.MethodPost, path: "/api/v1/specs/" + spec.ID + "/versions", body: map[string]string{}})
		r.expect(t, http.StatusCreated, "")
		r.decode(t, &v)
		return "/api/v1/versions/" + v.ID + "/code"
	}

	r := h.do(call{method: http.MethodGet, path: publish("Pairs", pairsSpec) + "?target=go-client"})
	r.expect(t, http.StatusOK, "")
	if r.header.Get("X-Codegen-Warnings") != "1" {
		t.Errorf("X-Codegen-Warnings = %q", r.header.Get("X-Codegen-Warnings"))
	}
	if w := unzip(t, r.body)["pairs/WARNINGS.md"]; !strings.Contains(w, "Line 22, column 17: prefixItems isn't expressed") {
		t.Errorf("WARNINGS.md = %q", w)
	}

	// A reference into another schema's properties is valid OpenAPI, but
	// oapi-codegen can't make a Go type of it.
	h.do(call{method: http.MethodGet, path: publish("Nested", nestedRefSpec) + "?target=go-client"}).
		expect(t, http.StatusUnprocessableEntity, "codegen-failed")
}

const pairsSpec = `openapi: 3.1.1
info:
  title: Pairs API
  version: 1.0.0
  description: Pairs.
servers: [{url: 'https://pairs.example.com'}]
tags: [{name: pairs, description: Pairs.}]
paths:
  /pairs:
    get:
      operationId: listPairs
      summary: List pairs.
      description: Lists pairs.
      tags: [pairs]
      responses:
        '200':
          description: The pairs.
          content:
            application/json:
              schema:
                type: array
                prefixItems: [{type: string}, {type: integer}]
`

const nestedRefSpec = `openapi: 3.1.1
info:
  title: Nested API
  version: 1.0.0
  description: Nested.
servers: [{url: 'https://nested.example.com'}]
tags: [{name: things, description: Things.}]
paths:
  /things:
    get:
      operationId: getThing
      summary: Get a thing.
      description: Gets a thing.
      tags: [things]
      responses:
        '200':
          description: The thing.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
components:
  schemas:
    Thing:
      type: object
      properties:
        copy: {$ref: '#/components/schemas/Thing/properties/name'}
        name: {type: string}
`
