// SPDX-License-Identifier: Apache-2.0

// Command demoshop is a small web shop that sends real traces to
// Signalbox, for trying Signalbox and for its own tests. One binary runs
// any of its parts:
//
//	demoshop storefront   the shop's web API, which browsers call
//	demoshop orders       orders, kept in PostgreSQL
//	demoshop inventory    products and their stock
//	demoshop payments     charges, through the payment provider
//	demoshop psp          a payment provider outside the shop, not traced
//	demoshop shopper      people browsing and buying, a few times a second
//	demoshop seed         sets the shop up in Signalbox and makes its ingest key
//
// See README.md, and deploy/compose/compose.demo.yaml to run it all.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: demoshop storefront|orders|inventory|payments|psp|shopper|seed")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch part := os.Args[1]; part {
	case "storefront":
		err = runStorefront(ctx)
	case "orders":
		err = runOrders(ctx)
	case "inventory":
		err = runInventory(ctx)
	case "payments":
		err = runPayments(ctx)
	case "psp":
		err = runPSP(ctx)
	case "shopper":
		err = runShopper(ctx)
	case "seed":
		err = runSeed(ctx)
	default:
		err = fmt.Errorf("unknown part %q", part)
	}
	if err != nil {
		logger.Error("demoshop stopped", "error", err)
		os.Exit(1)
	}
}

// env returns an environment variable, or fallback when it's unset.
func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
