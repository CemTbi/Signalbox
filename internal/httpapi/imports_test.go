// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestImportingAndExportingCollections(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var file map[string]any
	data, err := os.ReadFile("../modules/client/convert/testdata/shop.postman_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	imports := "/api/v1/projects/" + p.ID + "/collection-imports"

	// A dry run reports and stores nothing.
	var dry struct {
		Collection *httpapi.CollectionJSON `json:"collection"`
		Report     httpapi.ReportJSON      `json:"report"`
	}
	r := h.do(call{method: http.MethodPost, path: imports + "?dryRun=true", body: file})
	r.expect(t, http.StatusOK, "")
	r.decode(t, &dry)
	if dry.Collection != nil || dry.Report.Requests != 6 || dry.Report.Clean || len(dry.Report.Notes) == 0 {
		t.Errorf("dry run = %+v", dry)
	}
	var list struct {
		Data []httpapi.CollectionJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/collections"}).decode(t, &list)
	if len(list.Data) != 0 {
		t.Errorf("a dry run stored %+v", list.Data)
	}

	r = h.do(call{method: http.MethodPost, path: imports, body: file})
	r.expect(t, http.StatusCreated, "")
	var done struct {
		Collection httpapi.CollectionJSON `json:"collection"`
		Report     httpapi.ReportJSON     `json:"report"`
	}
	r.decode(t, &done)
	if done.Collection.Name != "Shop API" || len(done.Collection.Variables) != 2 || done.Collection.Scripts == nil ||
		r.header.Get("Location") != "/api/v1/collections/"+done.Collection.ID {
		t.Errorf("import = %+v", done)
	}

	r = h.do(call{method: http.MethodGet, path: "/api/v1/collections/" + done.Collection.ID + "/export"})
	r.expect(t, http.StatusOK, "")
	if cd := r.header.Get("Content-Disposition"); cd != `attachment; filename="Shop API.postman_collection.json"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	var exported struct {
		Info struct {
			Schema string `json:"schema"`
		} `json:"info"`
		Item []map[string]any `json:"item"`
	}
	r.decode(t, &exported)
	if !strings.Contains(exported.Info.Schema, "v2.1.0") || len(exported.Item) != 5 {
		t.Errorf("export = %+v", exported)
	}

	// Collection variables change through PATCH.
	r = h.do(call{method: http.MethodPatch, path: "/api/v1/collections/" + done.Collection.ID, contentType: "application/merge-patch+json",
		body: map[string]any{"variables": []map[string]string{{"key": "baseUrl", "value": "https://v2.example.com"}}}})
	r.expect(t, http.StatusOK, "")
	var patched httpapi.CollectionJSON
	r.decode(t, &patched)
	if len(patched.Variables) != 1 || patched.Variables[0].Value != "https://v2.example.com" {
		t.Errorf("patched = %+v", patched)
	}

	h.do(call{method: http.MethodPost, path: imports, body: map[string]any{"id": "x", "requests": []any{}}}).expect(t, http.StatusUnprocessableEntity, "not-postman")
	h.do(call{method: http.MethodPost, path: imports + "?dryRun=maybe", body: file, invalid: true}).expect(t, http.StatusBadRequest, "validation-failed")
}

func TestImportingEnvironmentsAndCurl(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")
	var envs struct {
		Data []httpapi.EnvironmentJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/environments"}).decode(t, &envs)
	staging := envs.Data[1]
	file := map[string]any{"name": "Staging", "_postman_variable_scope": "environment", "values": []map[string]any{
		{"key": "baseUrl", "value": "https://staging.example.com", "enabled": true},
		{"key": "token", "value": "s3cret", "type": "secret", "enabled": true},
	}}
	r := h.do(call{method: http.MethodPost, path: "/api/v1/environments/" + staging.ID + "/variable-imports", body: file})
	r.expect(t, http.StatusOK, "")
	var done struct {
		Variables httpapi.VariablesJSON `json:"variables"`
		Report    httpapi.ReportJSON    `json:"report"`
	}
	r.decode(t, &done)
	if len(done.Variables.Variables) != 2 || !done.Variables.Variables[1].Secret || !done.Report.Clean || strings.Contains(string(r.body), "s3cret") {
		t.Errorf("import = %s", r.body)
	}

	r = h.do(call{method: http.MethodPost, path: "/api/v1/curl-imports", body: map[string]string{
		"command": `curl -X POST https://api.example.com/orders -H 'Content-Type: application/json' -d '{"sku":"A-1"}' -u ada:pw --insecure`,
	}})
	r.expect(t, http.StatusOK, "")
	var curl struct {
		Request httpapi.SavedRequestJSON `json:"request"`
		Report  httpapi.ReportJSON       `json:"report"`
	}
	r.decode(t, &curl)
	if curl.Request.Method != http.MethodPost || curl.Request.Body != `{"sku":"A-1"}` || curl.Request.Auth == nil || curl.Request.Auth.Username != "ada" ||
		curl.Report.Clean || len(curl.Report.Notes) != 1 {
		t.Errorf("curl = %+v", curl)
	}
	h.do(call{method: http.MethodPost, path: "/api/v1/curl-imports", body: map[string]string{"command": "curl -H"}}).expect(t, http.StatusUnprocessableEntity, "not-curl")
}
