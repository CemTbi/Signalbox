// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

var (
	// ErrBusy means the writer holds as many spans as it may: send them
	// again later.
	ErrBusy = errors.New("telemetry: too many spans waiting to be written")
	// ErrClosed means the writer has stopped.
	ErrClosed = errors.New("telemetry: the span writer has stopped")
)

// WriterOptions tune a Writer. Zero values take the defaults.
type WriterOptions struct {
	// MaxBatch is how many spans make an insert at once. Default 10,000.
	MaxBatch int
	// Interval is the longest a span waits for its batch to fill. Default
	// one second.
	Interval time.Duration
	// MaxPending bounds the spans waiting or being written. Default 200,000.
	MaxPending int
}

// Writer batches spans from many requests into few inserts, as ClickHouse
// wants: one insert a second, or sooner once MaxBatch spans wait. Write
// returns once its spans are stored, so a request can be acknowledged only
// then. It is safe for concurrent use.
type Writer struct {
	conn   clickhouse.Conn
	logger *slog.Logger
	opts   WriterOptions

	in      chan *submission
	pending atomic.Int64
	quit    chan struct{}
	done    chan struct{}
	close   sync.Once

	written  *metrics.Counter
	failed   *metrics.Counter
	duration *metrics.Histogram
}

type submission struct {
	spans []Span
	// result takes the batch's outcome; it has room for it.
	result chan error
}

// NewWriter starts a writer. Close stops it.
func NewWriter(conn clickhouse.Conn, logger *slog.Logger, opts WriterOptions, reg *metrics.Registry) *Writer {
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 10_000
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = 200_000
	}
	w := &Writer{
		conn: conn, logger: logger, opts: opts,
		in: make(chan *submission, 1024), quit: make(chan struct{}), done: make(chan struct{}),
		written: reg.NewCounter("signalbox_ingest_spans_written_total", "Spans stored in ClickHouse."),
		failed:  reg.NewCounter("signalbox_ingest_inserts_failed_total", "Span inserts ClickHouse refused or didn't answer; their senders were told to retry."),
		duration: reg.NewHistogram("signalbox_ingest_insert_duration_seconds", "How long span inserts took.",
			metrics.DurationBuckets),
	}
	reg.NewGaugeFunc("signalbox_ingest_spans_pending", "Spans waiting to be written, or being written.",
		func() float64 { return float64(w.pending.Load()) })
	go w.run()
	return w
}

// Write stores spans, batched with others, and returns once they are
// stored. It returns ErrBusy at once when too many spans wait already.
// When ctx ends first, the spans may still be stored.
func (w *Writer) Write(ctx context.Context, spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	select {
	case <-w.quit:
		return ErrClosed
	default:
	}
	n := int64(len(spans))
	if w.pending.Add(n) > int64(w.opts.MaxPending) {
		w.pending.Add(-n)
		return ErrBusy
	}
	sub := &submission{spans: spans, result: make(chan error, 1)}
	select {
	case w.in <- sub:
	case <-w.quit:
		w.pending.Add(-n)
		return ErrClosed
	case <-ctx.Done():
		w.pending.Add(-n)
		return ctx.Err()
	}
	select {
	case err := <-sub.result:
		return err
	case <-w.done:
		// It may have been written in the last batch, or have arrived
		// after the writer drained its queue.
		select {
		case err := <-sub.result:
			return err
		default:
			w.pending.Add(-n)
			return ErrClosed
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops taking spans, writes those it has, and waits until it has.
func (w *Writer) Close() {
	w.close.Do(func() { close(w.quit) })
	<-w.done
}

func (w *Writer) run() {
	defer close(w.done)
	var (
		batch []*submission
		count int
		timer *time.Timer
		fire  <-chan time.Time
	)
	flush := func() {
		if timer != nil {
			timer.Stop()
			timer, fire = nil, nil
		}
		if len(batch) == 0 {
			return
		}
		w.insert(batch, count)
		batch, count = nil, 0
	}
	add := func(sub *submission) {
		if len(batch) == 0 {
			timer = time.NewTimer(w.opts.Interval)
			fire = timer.C
		}
		batch = append(batch, sub)
		count += len(sub.spans)
		if count >= w.opts.MaxBatch {
			flush()
		}
	}
	for {
		select {
		case sub := <-w.in:
			add(sub)
		case <-fire:
			flush()
		case <-w.quit:
			// Senders that got in before the quit are written too.
			for {
				select {
				case sub := <-w.in:
					add(sub)
				default:
					flush()
					return
				}
			}
		}
	}
}

// insert writes one batch and tells each sender how it went.
func (w *Writer) insert(batch []*submission, count int) {
	spans := make([]Span, 0, count)
	for _, sub := range batch {
		spans = append(spans, sub.spans...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	err := WriteSpans(ctx, w.conn, spans)
	w.duration.Observe(time.Since(start).Seconds())
	if err != nil {
		w.failed.Inc()
		w.logger.Error("couldn't store spans; their senders will retry", "spans", count, "error", err)
	} else {
		w.written.Add(float64(count))
	}
	for _, sub := range batch {
		sub.result <- err
	}
	w.pending.Add(-int64(count))
}
