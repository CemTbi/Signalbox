// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// Chaos modes break payments on purpose, so monitors, alerts and the
// canvas have something to show.
const (
	chaosOff       = "off"       // everything works
	chaosSlow      = "slow"      // each payment waits 1.5 s more
	chaosFailing   = "failing"   // half the payments fail
	chaosUnhealthy = "unhealthy" // payments work, but the health check fails
)

type payments struct {
	psp string

	mu    sync.Mutex
	chaos string
}

func runPayments(ctx context.Context) error {
	shutdown, err := startTracing(ctx, "payments-api")
	if err != nil {
		return err
	}
	defer shutdown(context.Background()) //nolint:errcheck // spans in flight are best effort at exit
	p := &payments{psp: env("DEMO_PSP_URL", "http://psp.example:8080"), chaos: env("DEMO_CHAOS", chaosOff)}
	return serve(ctx, env("DEMO_ADDR", ":8080"), p.routes())
}

func (p *payments) mode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.chaos
}

func (p *payments) routes() *router {
	rt := newRouter()
	rt.handle("POST /payments", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			OrderID     string `json:"orderId"`
			AmountCents int    `json:"amountCents"`
		}
		if err := decode(r, &in); err != nil || in.AmountCents < 1 {
			problem(w, r, http.StatusBadRequest, "Send the orderId and a positive amountCents as JSON.")
			return
		}
		switch p.mode() {
		case chaosSlow:
			time.Sleep(1500 * time.Millisecond)
		case chaosFailing:
			if rand.IntN(2) == 0 { //nolint:gosec // G404: a demo's coin toss
				problem(w, r, http.StatusBadGateway, "The payment provider refused the charge.")
				return
			}
		}
		var charge struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		err := call(r.Context(), http.MethodPost, p.psp+"/v1/charges", map[string]any{"amount": in.AmountCents, "currency": "eur", "reference": in.OrderID}, &charge, http.StatusCreated)
		if err != nil {
			problem(w, r, http.StatusBadGateway, "The payment provider didn't answer: "+err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": charge.ID, "status": charge.Status})
	})
	// The chaos switch: PUT {"mode": "slow"}, or GET to read it.
	rt.handle("GET /chaos", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"mode": p.mode()})
	})
	rt.handle("PUT /chaos", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Mode string `json:"mode"`
		}
		if err := decode(r, &in); err != nil {
			problem(w, r, http.StatusBadRequest, "Send the mode as JSON.")
			return
		}
		switch in.Mode {
		case chaosOff, chaosSlow, chaosFailing, chaosUnhealthy:
		default:
			problem(w, r, http.StatusBadRequest, "Use off, slow, failing or unhealthy.")
			return
		}
		p.mu.Lock()
		p.chaos = in.Mode
		p.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"mode": in.Mode})
	})
	rt.handle("GET /health", healthHandler("payments-api",
		check{"psp:responseTime", func(ctx context.Context) (string, any, string, string) {
			start := time.Now()
			if !reachable(ctx, p.psp+"/v1/ping") {
				return "fail", nil, "", "The payment provider doesn't answer."
			}
			ms := float64(time.Since(start).Microseconds()) / 1000
			if ms > 500 {
				return "warn", ms, "ms", "The payment provider is slow."
			}
			return "pass", ms, "ms", ""
		}},
		check{"payments:chaos", func(context.Context) (string, any, string, string) {
			switch p.mode() {
			case chaosUnhealthy:
				return "fail", nil, "", "Payments are switched to unhealthy."
			case chaosSlow, chaosFailing:
				return "warn", nil, "", "Payments are switched to " + p.mode() + "."
			}
			return "pass", nil, "", ""
		}},
	))
	return rt
}

// runPSP is a payment provider outside the shop: it isn't traced, so
// Signalbox sees it only from the payments service's calls, as an
// external host.
func runPSP(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /v1/charges", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(40+rand.IntN(120)) * time.Millisecond) //nolint:gosec // G404: a demo's latency
		writeJSON(w, http.StatusCreated, map[string]string{"id": "ch_" + randomID(), "status": "succeeded"})
	})
	srv := &http.Server{Addr: env("DEMO_ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// reachable says whether url answers 200. Health checks aren't traced, so
// it uses a plain client: a traced one would start a trace of its own.
func reachable(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func randomID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 16)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))] //nolint:gosec // G404: IDs in a demo
	}
	return string(b)
}
