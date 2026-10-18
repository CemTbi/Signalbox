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
