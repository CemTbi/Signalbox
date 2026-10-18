// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/client"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

func names(items []client.Item, parent uuid.UUID) []string {
	var out []string
	for _, it := range items {
		if it.ParentID == parent {
			out = append(out, it.Name)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCollectionsAndItems(t *testing.T) {
	f := setup(t)
	c, err := f.mod.CreateCollection(f.ctx, f.owner, f.project, " Orders API ", "Everything about orders.")
	if err != nil || c.Name != "Orders API" || c.Version != 1 {
		t.Fatalf("collection = %+v, %v", c, err)
	}
	folder, err := f.mod.CreateItem(f.ctx, f.owner, c.ID, client.ItemInput{Kind: client.KindFolder, Name: "Orders"})
	if err != nil {
		t.Fatal(err)
	}
	req := func(name, url string, parent uuid.UUID, before uuid.UUID) client.Item {
		t.Helper()
		it, err := f.mod.CreateItem(f.ctx, f.owner, c.ID, client.ItemInput{
			Kind: client.KindRequest, Name: name, Placement: client.Placement{Parent: parent, Before: before},
			Request: &client.SavedRequest{Method: "get", URL: url, Headers: []client.SavedHeader{{Key: "Accept", Value: "application/json", Enabled: true}}},
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return it
	}
	list := req("List orders", "{{baseUrl}}/orders", folder.ID, uuid.Nil)
	get := req("Get an order", "{{baseUrl}}/orders/1", folder.ID, uuid.Nil)
	first := req("First", "{{baseUrl}}/first", folder.ID, list.ID)
	health := req("Health", "{{baseUrl}}/health", uuid.Nil, uuid.Nil)

	items, err := f.mod.Items(f.ctx, f.owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(names(items, uuid.Nil), []string{"Orders", "Health"}) || !equal(names(items, folder.ID), []string{"First", "List orders", "Get an order"}) {
		t.Fatalf("tree = %+v", items)
	}
	// Lists carry each request's method and URL, not its headers.
	for _, it := range items {
		if it.ID == get.ID && (it.Request == nil || it.Request.Method != http.MethodGet || it.Request.URL != "{{baseUrl}}/orders/1" || len(it.Request.Headers) != 0) {
			t.Errorf("listed request = %+v", it.Request)
		}
	}
	full, err := f.mod.GetItem(f.ctx, f.owner, get.ID)
	if err != nil || len(full.Request.Headers) != 1 || !full.Request.Headers[0].Enabled {
		t.Errorf("item = %+v, %v", full, err)
	}

	// Moving: to the top before Health, then back into the folder at its end.
	name := "Get one order"
	moved, err := f.mod.UpdateItem(f.ctx, f.owner, get.ID, workspaces.Precondition{Versions: []int64{1}},
		client.ItemPatch{Name: &name, Move: &client.Placement{Before: health.ID}})
	if err != nil || moved.Version != 2 || moved.ParentID != uuid.Nil || moved.Name != name {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	items, _ = f.mod.Items(f.ctx, f.owner, c.ID)
	if !equal(names(items, uuid.Nil), []string{"Orders", "Get one order", "Health"}) || !equal(names(items, folder.ID), []string{"First", "List orders"}) {
		t.Errorf("after the move: %v / %v", names(items, uuid.Nil), names(items, folder.ID))
	}
	if _, err := f.mod.UpdateItem(f.ctx, f.owner, get.ID, workspaces.Precondition{Versions: []int64{1}}, client.ItemPatch{Name: &name}); !errors.Is(err, client.ErrVersionMismatch) {
		t.Errorf("stale version: %v", err)
	}

	// A folder can't go inside itself, or into a request.
	sub, err := f.mod.CreateItem(f.ctx, f.owner, c.ID, client.ItemInput{Kind: client.KindFolder, Name: "Sub", Placement: client.Placement{Parent: folder.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var verrs validate.Errors
	if _, err := f.mod.UpdateItem(f.ctx, f.owner, folder.ID, workspaces.Precondition{}, client.ItemPatch{Move: &client.Placement{Parent: sub.ID}}); !errors.As(err, &verrs) {
		t.Errorf("folder into its child: %v", err)
	}
	if _, err := f.mod.UpdateItem(f.ctx, f.owner, first.ID, workspaces.Precondition{}, client.ItemPatch{Move: &client.Placement{Parent: health.ID}}); !errors.As(err, &verrs) {
		t.Errorf("item into a request: %v", err)
	}
	other, _ := f.mod.CreateCollection(f.ctx, f.owner, f.project, "Other", "")
	if _, err := f.mod.CreateItem(f.ctx, f.owner, other.ID, client.ItemInput{Kind: client.KindFolder, Name: "X", Placement: client.Placement{Parent: folder.ID}}); !errors.As(err, &verrs) {
		t.Errorf("parent from another collection: %v", err)
	}

	// Deleting a folder deletes what's in it.
	if err := f.mod.DeleteItem(f.ctx, f.owner, folder.ID, workspaces.Precondition{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.GetItem(f.ctx, f.owner, list.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("an item of a deleted folder: %v", err)
	}
	all, _ := f.mod.ListCollections(f.ctx, f.owner, f.project)
	if len(all) != 2 || all[0].Name != "Orders API" || all[1].Name != "Other" {
		t.Errorf("collections = %+v", all)
	}
	if err := f.mod.DeleteCollection(f.ctx, f.owner, c.ID, workspaces.Precondition{Versions: []int64{1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.GetItem(f.ctx, f.owner, health.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("an item of a deleted collection: %v", err)
	}
}

func TestCollectionRoles(t *testing.T) {
	f := setup(t)
	viewer := f.member(t, "linus", workspaces.Viewer)
	editor := f.member(t, "grace", workspaces.Editor)
	stranger := f.user(t, "mallory")
	c, err := f.mod.CreateCollection(f.ctx, editor, f.project, "Shared", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.CreateCollection(f.ctx, viewer, f.project, "No", ""); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer creates: %v", err)
	}
	it, err := f.mod.CreateItem(f.ctx, editor, c.ID, client.ItemInput{Kind: client.KindFolder, Name: "F"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.mod.Items(f.ctx, viewer, c.ID); err != nil {
		t.Errorf("viewer reads: %v", err)
	}
	if err := f.mod.DeleteItem(f.ctx, viewer, it.ID, workspaces.Precondition{}); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("viewer deletes an item: %v", err)
	}
	if _, err := f.mod.GetCollection(f.ctx, stranger, c.ID); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("stranger reads: %v", err)
	}
	// Deleting a whole collection needs an owner or admin, as for specs.
	if err := f.mod.DeleteCollection(f.ctx, editor, c.ID, workspaces.Precondition{}); !errors.Is(err, client.ErrForbidden) {
		t.Errorf("editor deletes the collection: %v", err)
	}
	var verrs validate.Errors
	if _, err := f.mod.CreateItem(f.ctx, editor, c.ID, client.ItemInput{Kind: "request", Name: "No request"}); !errors.As(err, &verrs) {
		t.Errorf("request without a request: %v", err)
	}
}
