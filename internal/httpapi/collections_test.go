// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/CemTbi/Signalbox/internal/httpapi"
)

func TestCollections(t *testing.T) {
	h := newHarness(t)
	h.setUp()
	p := h.newProject("shop")

	r := h.do(call{method: http.MethodPost, path: "/api/v1/projects/" + p.ID + "/collections", body: map[string]string{"name": "Orders API"}})
	r.expect(t, http.StatusCreated, "")
	var c httpapi.CollectionJSON
	r.decode(t, &c)
	items := "/api/v1/collections/" + c.ID + "/items"

	var folder httpapi.ItemJSON
	h.do(call{method: http.MethodPost, path: items, body: map[string]any{"kind": "folder", "name": "Orders"}}).decode(t, &folder)
	r = h.do(call{method: http.MethodPost, path: items, body: map[string]any{
		"kind": "request", "name": "Get an order", "parentId": folder.ID,
		"request": map[string]any{"method": "GET", "url": "{{baseUrl}}/orders/1", "headers": []map[string]any{{"key": "Accept", "value": "application/json", "enabled": true}}},
	}})
	r.expect(t, http.StatusCreated, "")
	var get httpapi.ItemJSON
	r.decode(t, &get)
	tag := r.header.Get("ETag")
	if get.ParentID != folder.ID || get.Request == nil || get.Request.URL != "{{baseUrl}}/orders/1" {
		t.Fatalf("item = %+v", get)
	}

	var list struct {
		Data []httpapi.ItemJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: items}).decode(t, &list)
	if len(list.Data) != 2 || list.Data[0].ID != folder.ID || list.Data[1].Request == nil || len(list.Data[1].Request.Headers) != 0 {
		t.Errorf("items = %+v", list)
	}

	// Save new content, then move it to the top, with the ETag.
	r = h.do(call{method: http.MethodPatch, path: "/api/v1/items/" + get.ID, contentType: "application/merge-patch+json", headers: map[string]string{"If-Match": tag},
		body: map[string]any{"request": map[string]any{"method": "POST", "url": "{{baseUrl}}/orders", "headers": []map[string]any{}, "body": "{}"}, "parentId": nil}})
	r.expect(t, http.StatusOK, "")
	var saved httpapi.ItemJSON
	r.decode(t, &saved)
	if saved.ParentID != "" || saved.Request.Method != http.MethodPost || saved.Request.Body != "{}" {
		t.Errorf("saved = %+v", saved)
	}
	h.do(call{method: http.MethodPatch, path: "/api/v1/items/" + get.ID, contentType: "application/merge-patch+json", headers: map[string]string{"If-Match": tag},
		body: map[string]any{"name": "Stale"}}).expect(t, http.StatusPreconditionFailed, "edit-conflict")
	// A folder can't go inside a request.
	h.do(call{method: http.MethodPatch, path: "/api/v1/items/" + folder.ID, contentType: "application/merge-patch+json",
		body: map[string]any{"parentId": get.ID}}).expect(t, http.StatusBadRequest, "validation-failed")

	h.do(call{method: http.MethodDelete, path: "/api/v1/items/" + folder.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: "/api/v1/items/" + folder.ID}).expect(t, http.StatusNotFound, "not-found")
	var cols struct {
		Data []httpapi.CollectionJSON `json:"data"`
	}
	h.do(call{method: http.MethodGet, path: "/api/v1/projects/" + p.ID + "/collections"}).decode(t, &cols)
	if len(cols.Data) != 1 || cols.Data[0].Name != "Orders API" {
		t.Errorf("collections = %+v", cols)
	}
	h.do(call{method: http.MethodDelete, path: "/api/v1/collections/" + c.ID}).expect(t, http.StatusNoContent, "")
	h.do(call{method: http.MethodGet, path: items}).expect(t, http.StatusNotFound, "not-found")
}
