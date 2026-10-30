// SPDX-License-Identifier: Apache-2.0

// Package ingest serves the ingest role: it receives spans over OTLP/HTTP
// and Zipkin v2, checks the ingest key each request carries, and hands
// the spans to the telemetry module to store. Each protocol answers as its
// specification says, so exporters retry when they should and only then.
// See docs/adr/0040-span-ingest.md.
package ingest

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
	"github.com/CemTbi/Signalbox/internal/platform/ratelimit"
)

// Defaults for Options.
const (
	DefaultSpansPerSecond = 5000
	DefaultMaxBodyBytes   = 16 << 20
)

// Options tune the ingest role.
type Options struct {
	// SpansPerSecond is each environment's rate limit. Up to twice as many
	// may arrive at once.
	SpansPerSecond int
	// RetentionDays is how long spans are kept: older spans are refused,
	// since they would be dropped at once.
	RetentionDays int
	// MaxBodyBytes bounds a request's body, once decompressed.
	MaxBodyBytes int64
}

// Keys finds who sends with an ingest key; *telemetry.Keys is one.
type Keys interface {
	Authenticate(ctx context.Context, key string) (telemetry.Sender, error)
}

// Spans stores spans; *telemetry.Writer is one.
type Spans interface {
	Write(ctx context.Context, spans []telemetry.Span) error
}

// Server handles the ingest protocols.
type Server struct {
	keys     Keys
	spans    Spans
	prep     *telemetry.Preparer
	logger   *slog.Logger
	opts     Options
	now      func() time.Time
	perEnv   *ratelimit.Limiter
	badKeyIP *ratelimit.Limiter

	requests *metrics.Counter
	accepted *metrics.Counter
	refused  *metrics.Counter
	dropped  *metrics.Counter
}

// New returns the ingest server.
func New(keys Keys, spans Spans, logger *slog.Logger, opts Options, reg *metrics.Registry) *Server {
	if opts.SpansPerSecond <= 0 {
		opts.SpansPerSecond = DefaultSpansPerSecond
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = telemetry.DefaultRetentionDays
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return &Server{
		keys: keys, spans: spans, prep: telemetry.NewPreparer(), logger: logger, opts: opts, now: time.Now,
		perEnv: ratelimit.New(2*opts.SpansPerSecond, time.Second/time.Duration(opts.SpansPerSecond)),
		// Requests with keys that don't exist: 20 at once, then 1 a second
		// per address, so guessing costs time and the database little.
		badKeyIP: ratelimit.New(20, time.Second),
		requests: reg.NewCounter("signalbox_ingest_requests_total",
			`Requests to the ingest listeners, by protocol ("otlp_http" or "zipkin") and outcome.`, "protocol", "outcome"),
		accepted: reg.NewCounter("signalbox_ingest_spans_accepted_total", "Spans received and stored, by protocol.", "protocol"),
		refused: reg.NewCounter("signalbox_ingest_spans_refused_total",
			`Spans not stored, by protocol and reason: "invalid", "throttled", "busy" or "failed". Senders retry all but invalid ones.`,
			"protocol", "reason"),
		dropped: reg.NewCounter("signalbox_ingest_span_data_dropped_total",
			`Parts of stored spans left out to keep within the limits: "attributes", "events", "links", or "truncated" values.`, "what"),
	}
}

// window is the start times ingest accepts: within retention, and at most
// an hour ahead, for clocks that run fast. Each distinct day makes a
// partition, so far-off times would cost ClickHouse dearly.
type window struct{ from, to time.Time }

func (s *Server) window() window {
	now := s.now()
	return window{from: now.AddDate(0, 0, -s.opts.RetentionDays), to: now.Add(time.Hour)}
}

func (w window) check(t time.Time) error {
	switch {
	case t.Before(w.from):
		return fmt.Errorf("%w: a span started %s, before the retention period", errInvalidSpan, t.Format(time.RFC3339))
	case t.After(w.to):
		return fmt.Errorf("%w: a span starts %s, more than an hour from now", errInvalidSpan, t.Format(time.RFC3339))
	}
	return nil
}

// result is how a request ended, for its protocol to answer.
type result struct {
	status     int
	retryAfter time.Duration
	message    string
	// rejected spans were invalid; why says why the first was.
	rejected int
	why      string
}

// decoder reads a request body of a media type into spans.
type decoder func(body []byte, mediaType string, accept window) (spans []telemetry.Span, rejected int, why string, err error)

// ingest runs a request through the steps both protocols share.
func (s *Server) ingest(r *http.Request, protocol string, decode decoder) result {
	sender, res, ok := s.authenticate(r)
	if !ok {
		s.requests.Inc(protocol, "unauthorized")
		return res
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		s.requests.Inc(protocol, "unsupported")
		return result{status: http.StatusUnsupportedMediaType, message: "Send a Content-Type the protocol defines."}
	}
	body, res, ok := s.readBody(r)
	if !ok {
		s.requests.Inc(protocol, "invalid")
		return res
	}
	spans, rejected, why, err := decode(body, mediaType, s.window())
	if errors.Is(err, errUnsupported) {
		s.requests.Inc(protocol, "unsupported")
		return result{status: http.StatusUnsupportedMediaType, message: err.Error()}
	}
	if err != nil {
		s.requests.Inc(protocol, "invalid")
		return result{status: http.StatusBadRequest, message: err.Error()}
	}
	if rejected > 0 {
		s.refused.Add(float64(rejected), protocol, "invalid")
	}
	if len(spans) > 0 {
		if res, ok := s.store(r.Context(), protocol, sender.EnvironmentID, spans); !ok {
			return res
		}
	}
	outcome := "ok"
	if rejected > 0 {
		outcome = "partial"
	}
	s.requests.Inc(protocol, outcome)
	return result{status: http.StatusOK, rejected: rejected, why: why}
}

// authenticate checks the request's ingest key.
func (s *Server) authenticate(r *http.Request) (telemetry.Sender, result, bool) {
	key, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || key == "" {
		return telemetry.Sender{}, result{status: http.StatusUnauthorized,
			message: "Send an ingest key as Authorization: Bearer sbx_ing_…; project admins create them under Ingest keys."}, false
	}
	sender, err := s.keys.Authenticate(r.Context(), strings.TrimSpace(key))
	switch {
	case errors.Is(err, telemetry.ErrUnknownKey):
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ok, retry := s.badKeyIP.Allow(host); !ok {
			return sender, result{status: http.StatusTooManyRequests, retryAfter: retry, message: "Too many requests with unknown keys."}, false
		}
		return sender, result{status: http.StatusUnauthorized, message: "This ingest key doesn't exist or was revoked."}, false
	case err != nil:
		s.logger.ErrorContext(r.Context(), "couldn't check an ingest key", "error", err)
		return sender, result{status: http.StatusServiceUnavailable, retryAfter: 5 * time.Second, message: "Signalbox can't check keys right now."}, false
	}
	return sender, result{}, true
}

// readBody reads the body, decompressing gzip or zstd, and never more than
// MaxBodyBytes of it, so a small request can't expand into gigabytes.
func (s *Server) readBody(r *http.Request) ([]byte, result, bool) {
	limit := s.opts.MaxBodyBytes
	tooLarge := result{status: http.StatusRequestEntityTooLarge,
		message: fmt.Sprintf("Send at most %d MiB a request, once decompressed; exporters split larger batches.", limit>>20)}
	raw := http.MaxBytesReader(nil, r.Body, limit)
	var body io.Reader
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		body = raw
	case "gzip":
		zr, err := gzip.NewReader(raw)
		if err != nil {
			return nil, result{status: http.StatusBadRequest, message: "The body isn't gzip, as Content-Encoding says."}, false
		}
		defer zr.Close()
		body = zr
	case "zstd":
		zr, err := zstd.NewReader(raw, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(limit))) //nolint:gosec // G115: a positive size
		if err != nil {
			return nil, result{status: http.StatusBadRequest, message: "The body isn't zstd, as Content-Encoding says."}, false
		}
		defer zr.Close()
		body = zr
	default:
		return nil, result{status: http.StatusUnsupportedMediaType, message: "Send the body as is, or with gzip or zstd."}, false
	}
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes), int64(len(b)) > limit:
		return nil, tooLarge, false
	case err != nil:
		return nil, result{status: http.StatusBadRequest, message: "The body couldn't be read: " + err.Error()}, false
	}
	return b, result{}, true
}

// store limits, prepares and writes spans.
func (s *Server) store(ctx context.Context, protocol string, env uuid.UUID, spans []telemetry.Span) (result, bool) {
	n := float64(len(spans))
	if ok, retry := s.perEnv.AllowN(env.String(), len(spans)); !ok {
		s.requests.Inc(protocol, "throttled")
		s.refused.Add(n, protocol, "throttled")
		return result{status: http.StatusTooManyRequests, retryAfter: retry,
			message: fmt.Sprintf("This environment may send %d spans a second; send these again shortly.", s.opts.SpansPerSecond)}, false
	}
	var dropped telemetry.Dropped
	for i := range spans {
		spans[i].EnvironmentID = env
		dropped.Add(s.prep.Prepare(&spans[i]))
	}
	for what, count := range map[string]int{"attributes": dropped.Attributes, "events": dropped.Events, "links": dropped.Links, "truncated": dropped.Truncated} {
		if count > 0 {
			s.dropped.Add(float64(count), what)
		}
	}
	err := s.spans.Write(ctx, spans)
	switch {
	case err == nil:
		s.accepted.Add(n, protocol)
		return result{}, true
	case errors.Is(err, telemetry.ErrBusy):
		s.requests.Inc(protocol, "busy")
		s.refused.Add(n, protocol, "busy")
		return result{status: http.StatusServiceUnavailable, retryAfter: time.Second, message: "Signalbox is storing many spans; send these again shortly."}, false
	case ctx.Err() != nil:
		// The sender hung up; the spans may still be stored.
		s.requests.Inc(protocol, "canceled")
		return result{status: http.StatusServiceUnavailable, retryAfter: time.Second, message: "The request ended before the spans were stored."}, false
	default:
		s.requests.Inc(protocol, "failed")
		s.refused.Add(n, protocol, "failed")
		return result{status: http.StatusServiceUnavailable, retryAfter: 5 * time.Second, message: "Signalbox couldn't store the spans; send them again shortly."}, false
	}
}

// retryAfter sets Retry-After in whole seconds, at least one.
func retryAfter(w http.ResponseWriter, d time.Duration) {
	if d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int((d+time.Second-1)/time.Second))))
	}
}
