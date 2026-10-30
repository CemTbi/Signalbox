// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// runShopper plays people using the shop: each visit lists the products,
// and about a third of visits buy one or two of them and look at the
// order. It sends DEMO_VISITS_PER_SECOND visits a second, 3 by default.
// Like a browser, it isn't traced: traces start at the storefront.
func runShopper(ctx context.Context) error {
	base := env("DEMO_STOREFRONT_URL", "http://storefront-web:8080")
	perSecond, err := strconv.ParseFloat(env("DEMO_VISITS_PER_SECOND", "3"), 64)
	if err != nil || perSecond <= 0 || perSecond > 1000 {
		return fmt.Errorf("DEMO_VISITS_PER_SECOND: use a number from 0 to 1000")
	}
	browser := &http.Client{Timeout: 15 * time.Second}
	tick := time.NewTicker(time.Duration(float64(time.Second) / perSecond))
	defer tick.Stop()
	var visits, failed atomic.Int64
	report := time.NewTicker(time.Minute)
	defer report.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-report.C:
			slog.Info("shopping", "visits", visits.Load(), "failed", failed.Load())
		case <-tick.C:
			visits.Add(1)
			go func() {
				if err := visit(ctx, browser, base); err != nil && ctx.Err() == nil {
					failed.Add(1)
					slog.Debug("a visit failed", "error", err)
				}
			}()
		}
	}
}

func visit(ctx context.Context, browser *http.Client, base string) error {
	var products struct {
		Data []product `json:"data"`
	}
	if err := get(ctx, browser, base+"/api/products", &products); err != nil {
		return err
	}
	if len(products.Data) == 0 || rand.IntN(3) != 0 { //nolint:gosec // G404: shoppers' whims
		return nil
	}
	var items []orderItem
	for range 1 + rand.IntN(2) { //nolint:gosec // G404: shoppers' whims
		p := products.Data[rand.IntN(len(products.Data))]                        //nolint:gosec // G404: shoppers' whims
		items = append(items, orderItem{SKU: p.SKU, Quantity: 1 + rand.IntN(3)}) //nolint:gosec // G404: shoppers' whims
	}
	body, _ := json.Marshal(map[string]any{"items": items})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/checkout", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := browser.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ord order
	if resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("checkout: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&ord); err != nil {
		return err
	}
	return get(ctx, browser, base+"/api/orders/"+ord.ID, nil)
}

func get(ctx context.Context, browser *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := browser.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
