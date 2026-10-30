// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// runStorefront serves the shop's web API, which browsers call: it lists
// products from the inventory and places orders with the orders service.
func runStorefront(ctx context.Context) error {
	shutdown, err := startTracing(ctx, "storefront-web")
	if err != nil {
		return err
	}
	defer shutdown(context.Background()) //nolint:errcheck // spans in flight are best effort at exit
	return serve(ctx, env("DEMO_ADDR", ":8080"), storefrontRoutes(
		env("DEMO_INVENTORY_URL", "http://inventory-api:8080"), env("DEMO_ORDERS_URL", "http://orders-api:8080")))
}

func storefrontRoutes(inventory, orders string) *router {
	rt := newRouter()
	rt.handle("GET /api/products", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, http.MethodGet, inventory+"/products", nil, http.StatusOK)
	})
	rt.handle("POST /api/checkout", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, http.MethodPost, orders+"/orders", r.Body, http.StatusCreated)
	})
	rt.handle("GET /api/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, http.MethodGet, orders+"/orders/"+r.PathValue("id"), nil, http.StatusOK)
	})
	rt.handle("GET /health", healthHandler("storefront-web", check{"inventory:reachable", func(ctx context.Context) (string, any, string, string) {
		if !reachable(ctx, inventory+"/health") {
			return "warn", nil, "", "The inventory doesn't answer; the shop can't list products."
		}
		return "pass", nil, "", ""
	}}))
	return rt
}

// forward passes a request on and its answer back, as a backend for a
// frontend does.
func forward(w http.ResponseWriter, r *http.Request, method, url string, body io.Reader, want int) {
	req, err := http.NewRequestWithContext(r.Context(), method, url, body)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "The shop's backend didn't answer.")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != want && resp.StatusCode >= 500 {
		problem(w, r, http.StatusBadGateway, "The shop's backend failed.")
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, io.LimitReader(resp.Body, 1<<20)); err != nil && !errors.Is(err, io.EOF) {
		return // the browser left
	}
}
