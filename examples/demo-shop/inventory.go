// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

type product struct {
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
	Stock      int    `json:"stock"`
}

// inventory keeps products and their stock in memory, and restocks
// what runs out, so the shop never stops selling.
type inventory struct {
	mu       sync.Mutex
	products map[string]*product
	reserved atomic.Int64
}

func newInventory() *inventory {
	inv := &inventory{products: map[string]*product{}}
	for _, p := range []product{
		{"TEA-01", "Sencha, 100 g", 1290, 40},
		{"TEA-02", "Darjeeling, 100 g", 1590, 25},
		{"MUG-01", "Stoneware mug", 1800, 15},
		{"POT-01", "Cast iron teapot", 6400, 5},
		{"FLT-01", "Paper filters, 100", 450, 80},
	} {
		inv.products[p.SKU] = &p
	}
	return inv
}

func (inv *inventory) list() []product {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	out := make([]product, 0, len(inv.products))
	for _, p := range inv.products {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
	return out
}

// reserve takes stock for an order. Out of stock is refused, then
// restocked for the next shopper.
func (inv *inventory) reserve(sku string, quantity int) (product, error) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	p, ok := inv.products[sku]
	switch {
	case !ok:
		return product{}, fmt.Errorf("no product %s", sku)
	case quantity < 1:
		return product{}, fmt.Errorf("reserve at least one %s", sku)
	case p.Stock < quantity:
		p.Stock += 20
		return product{}, errOutOfStock
	}
	p.Stock -= quantity
	inv.reserved.Add(int64(quantity))
	return *p, nil
}

var errOutOfStock = errors.New("out of stock")

func runInventory(ctx context.Context) error {
	shutdown, err := startTracing(ctx, "inventory-api")
	if err != nil {
		return err
	}
	defer shutdown(context.Background()) //nolint:errcheck // spans in flight are best effort at exit
	return serve(ctx, env("DEMO_ADDR", ":8080"), inventoryRoutes(newInventory()))
}

func inventoryRoutes(inv *inventory) *router {
	rt := newRouter()
	rt.handle("GET /products", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": inv.list()})
	})
	rt.handle("POST /reservations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SKU      string `json:"sku"`
			Quantity int    `json:"quantity"`
		}
		if err := decode(r, &in); err != nil {
			problem(w, r, http.StatusBadRequest, "Send the sku and quantity as JSON.")
			return
		}
		p, err := inv.reserve(in.SKU, in.Quantity)
		switch {
		case errors.Is(err, errOutOfStock):
			problem(w, r, http.StatusConflict, in.SKU+" is out of stock.")
		case err != nil:
			problem(w, r, http.StatusBadRequest, err.Error())
		default:
			writeJSON(w, http.StatusCreated, map[string]any{"sku": p.SKU, "quantity": in.Quantity, "priceCents": p.PriceCents})
		}
	})
	rt.handle("GET /health", healthHandler("inventory-api", check{"stock:count", func(context.Context) (string, any, string, string) {
		total := 0
		for _, p := range inv.list() {
			total += p.Stock
		}
		return "pass", total, "items", ""
	}}))
	return rt
}
