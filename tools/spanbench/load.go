// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

// paced writes spans as they "happen", at rate spans per second for
// duration, in one insert per second, the way the ingest role will. It
// reports how long each insert took.
func paced(ctx context.Context, conn clickhouse.Conn, out io.Writer, seed uint64, env uuid.UUID, rate int, duration time.Duration) error {
	gen := newGenerator(seed, env)
	var (
		mu      sync.Mutex
		pending []telemetry.Span
	)
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	// Make spans every 50 ms, with start times just behind the clock.
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		begin := time.Now()
		made := 0
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				due := int(now.Sub(begin).Seconds() * float64(rate))
				var batch []telemetry.Span
				for made < due {
					spans := gen.trace(now.Add(-time.Duration(gen.r.IntN(50)) * time.Millisecond))
					batch = append(batch, spans...)
					made += len(spans)
				}
				mu.Lock()
				pending = append(pending, batch...)
				mu.Unlock()
			}
		}
	}()

	var (
		latencies []time.Duration
		written   int
		maxLag    time.Duration
	)
	flush := func() error {
		mu.Lock()
		batch := pending
		pending = nil
		mu.Unlock()
		if len(batch) == 0 {
			return nil
		}
		started := time.Now()
		// A fresh context, so the last flush isn't cut off by the deadline.
		writeCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := telemetry.WriteSpans(writeCtx, conn, batch); err != nil {
			return err
		}
		took := time.Since(started)
		latencies = append(latencies, took)
		written += len(batch)
		if lag := started.Sub(batch[0].Start); lag > maxLag {
			maxLag = lag
		}
		return nil
	}

	fmt.Fprintf(out, "writing %d spans/s for %s, one insert a second\n", rate, duration)
	begin := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	report := time.Now()
	for {
		select {
		case <-ctx.Done():
			<-produced
			if err := flush(); err != nil {
				return err
			}
			elapsed := time.Since(begin)
			fmt.Fprintf(out, "\nwrote %d spans in %s: %.0f spans/s in %d inserts\n",
				written, elapsed.Round(time.Second), float64(written)/elapsed.Seconds(), len(latencies))
			fmt.Fprintf(out, "insert latency: %s\n", summarize(latencies))
			fmt.Fprintf(out, "oldest span waited %s before its insert began\n", maxLag.Round(time.Millisecond))
			return nil
		case <-tick.C:
			if err := flush(); err != nil {
				return err
			}
			if time.Since(report) >= 30*time.Second {
				report = time.Now()
				recent := latencies[max(0, len(latencies)-30):]
				fmt.Fprintf(out, "%s  %d spans  last 30 inserts: %s\n",
					time.Since(begin).Round(time.Second), written, summarize(recent))
			}
		}
	}
}

// bulk fills [from, to) with spans at rate spans per second of simulated
// time, as fast as ClickHouse takes them, from workers in parallel. Each
// worker covers a contiguous slice of the time, so its inserts are about as
// local in time as live ingestion's.
func bulk(ctx context.Context, conn clickhouse.Conn, out io.Writer, seed uint64, env uuid.UUID, rate int, from, to time.Time, workers, batchSize int) error {
	total := int64(to.Sub(from).Seconds() * float64(rate))
	fmt.Fprintf(out, "writing about %d spans from %s to %s (%d spans/s of simulated time) with %d workers, %d spans per insert\n",
		total, from.Format(time.RFC3339), to.Format(time.RFC3339), rate, workers, batchSize)
	var written atomic.Int64
	begin := time.Now()

	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				n := written.Load()
				elapsed := time.Since(begin)
				rateNow := float64(n) / elapsed.Seconds()
				eta := time.Duration(float64(total-n)/rateNow) * time.Second
				fmt.Fprintf(out, "%s  %d spans (%.1f%%)  %.0f spans/s  %s left\n",
					elapsed.Round(time.Second), n, 100*float64(n)/float64(total), rateNow, eta.Round(time.Second))
			}
		}
	}()

	g, ctx := errgroup.WithContext(ctx)
	slice := to.Sub(from) / time.Duration(workers)
	var latMu sync.Mutex
	var latencies []time.Duration
	for w := range workers {
		g.Go(func() error {
			worker := uint64(w) //nolint:gosec // G115: w counts workers up from 0.
			gen := newGenerator(seed+worker*7919, env)
			arrivals := rand.New(rand.NewPCG(seed, worker)) //nolint:gosec // G404: seeded, so runs are reproducible.
			t := from.Add(time.Duration(w) * slice)
			end := t.Add(slice)
			batch := make([]telemetry.Span, 0, batchSize+64)
			send := func() error {
				started := time.Now()
				if err := telemetry.WriteSpans(ctx, conn, batch); err != nil {
					return err
				}
				latMu.Lock()
				latencies = append(latencies, time.Since(started))
				latMu.Unlock()
				written.Add(int64(len(batch)))
				batch = batch[:0]
				return nil
			}
			for t.Before(end) {
				spans := gen.trace(t)
				batch = append(batch, spans...)
				// Requests arrive as a Poisson process whose span rate is rate.
				gap := arrivals.ExpFloat64() * float64(len(spans)) / float64(rate)
				t = t.Add(time.Duration(gap * float64(time.Second)))
				if len(batch) >= batchSize {
					if err := send(); err != nil {
						return err
					}
				}
			}
			if len(batch) > 0 {
				return send()
			}
			return nil
		})
	}
	err := g.Wait()
	close(done)
	if err != nil {
		return err
	}
	elapsed := time.Since(begin)
	fmt.Fprintf(out, "\nwrote %d spans in %s: %.0f spans/s\n", written.Load(), elapsed.Round(time.Second),
		float64(written.Load())/elapsed.Seconds())
	fmt.Fprintf(out, "insert latency (%d spans each): %s\n", batchSize, summarize(latencies))
	return nil
}

// summarize describes a set of durations by their percentiles.
func summarize(d []time.Duration) string {
	if len(d) == 0 {
		return "none"
	}
	p := func(q float64) time.Duration { return percentile(d, q).Round(100 * time.Microsecond) }
	return fmt.Sprintf("p50 %s, p95 %s, p99 %s, max %s (n=%d)", p(0.5), p(0.95), p(0.99), p(1), len(d))
}
