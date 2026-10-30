// SPDX-License-Identifier: Apache-2.0

// Package runner sends requests on someone's behalf: for the API client,
// tests and, later, monitors. Every request goes through the egress dialer,
// so it can't reach Signalbox's own services, cloud metadata endpoints or
// loopback (docs/adr/0016-runner-egress.md). A run reports the response,
// how long each phase took, and what went wrong in words a person can act
// on. See docs/rfcs/0001-api-client-and-tests.md.
package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/CemTbi/Signalbox/internal/platform/egress"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

// Header is one header, in the order it was sent or received.
type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Request is a request ready to send: its variables already resolved.
type Request struct {
	Method  string
	URL     string
	Headers []Header
	Body    []byte
	// Timeout overrides the runner's for this request, up to its limit.
	Timeout time.Duration
}

// Timings say how long each phase of a request took. Phases that didn't
// happen, such as DNS for an address or TLS on a reused connection, are
// zero.
type Timings struct {
	DNS       time.Duration
	Connect   time.Duration
	TLS       time.Duration
	FirstByte time.Duration // from sending the request to the first byte of the response
	Download  time.Duration
	Total     time.Duration
}

// Response is what came back.
type Response struct {
	Status     int
	StatusText string
	Proto      string
	Headers    []Header
	// Body is at most Limits.MaxBody bytes; Truncated says whether there
	// was more.
	Body      []byte
	Truncated bool
	// URL is where the response came from, after redirects.
	URL        string
	Redirects  int
	RemoteAddr string
}

// Failure is why a request got no response.
type Failure struct {
	// Code is one of invalid-request, blocked, dns, connect, tls, timeout
	// or network.
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result is a request's outcome: a Response or a Failure, and the timings
// of whatever happened.
type Result struct {
	Response *Response
	Failure  *Failure
	Timings  Timings
}

// Limits bound what one request may take.
type Limits struct {
	// Timeout is the default and the most a request may ask for. Default
	// 30 seconds.
	Timeout time.Duration
	// MaxBody bounds the response body kept. Default 10 MiB.
	MaxBody int64
	// MaxRedirects bounds the redirects followed. Default 10.
	MaxRedirects int
}

// Runner sends requests. It is safe for concurrent use.
type Runner struct {
	client    *http.Client
	limits    Limits
	userAgent string
	metrics   *Metrics
}

// New returns a runner that sends requests through dialer.
func New(dialer *egress.Dialer, limits Limits, userAgent string) *Runner {
	if limits.Timeout <= 0 {
		limits.Timeout = 30 * time.Second
	}
	if limits.MaxBody <= 0 {
		limits.MaxBody = 10 << 20
	}
	if limits.MaxRedirects == 0 {
		limits.MaxRedirects = 10
	}
	// The egress client refuses bodies past its limit, even before reading
	// them; a runner keeps the start of a long body instead, so it reads
	// the limit itself.
	client := dialer.Client(egress.Limits{Timeout: limits.Timeout, MaxResponseBytes: math.MaxInt64, MaxRedirects: limits.MaxRedirects})
	return &Runner{client: client, limits: limits, userAgent: userAgent}
}

// Metrics counts the requests runners send, wherever they go out from.
type Metrics struct {
	requests *metrics.Counter
	duration *metrics.Histogram
}

// NewMetrics registers the request metrics. A nil registry counts nothing.
func NewMetrics(r *metrics.Registry) *Metrics {
	return &Metrics{
		requests: r.NewCounter("signalbox_runner_requests_total",
			`Requests sent, by where they went out from ("server" or "agent") and outcome: "response", or the failure code, such as "dns" or "agent-offline".`,
			"where", "outcome"),
		duration: r.NewHistogram("signalbox_runner_request_duration_seconds",
			"How long requests took, from handing them over to their result, by where they went out from.",
			metrics.DurationBuckets, "where"),
	}
}

// Observe records one request's result, sent from where, which took d.
func (m *Metrics) Observe(where string, res Result, d time.Duration) {
	if m == nil {
		return
	}
	outcome := "response"
	if res.Failure != nil {
		outcome = res.Failure.Code
	}
	m.requests.Inc(where, outcome)
	m.duration.Observe(d.Seconds(), where)
}

// UseMetrics counts the requests this runner sends as "server".
func (r *Runner) UseMetrics(m *Metrics) { r.metrics = m }

// Run sends one request. It never returns an error: what went wrong is in
// the Result's Failure.
func (r *Runner) Run(ctx context.Context, req Request) Result {
	start := time.Now()
	res := r.run(ctx, req)
	r.metrics.Observe("server", res, time.Since(start))
	return res
}

func (r *Runner) run(ctx context.Context, req Request) Result {
	httpReq, failure := r.build(ctx, req)
	if failure != nil {
		return Result{Failure: failure}
	}
	timeout := r.limits.Timeout
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tr := &tracer{redirects: -1}
	start := time.Now()
	resp, err := r.client.Do(httpReq.WithContext(httptrace.WithClientTrace(ctx, tr.trace())))
	if err != nil {
		t, _, _ := tr.snapshot()
		t.Total = time.Since(start)
		return Result{Failure: describe(err, ctx), Timings: t}
	}
	defer resp.Body.Close()

	firstByte := time.Now()
	// One byte past the limit tells a long body from one exactly at it.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, r.limits.MaxBody+1))
	t, remote, redirects := tr.snapshot()
	if readErr != nil {
		t.Total = time.Since(start)
		return Result{Failure: describe(readErr, ctx), Timings: t}
	}
	truncated := int64(len(body)) > r.limits.MaxBody
	if truncated {
		body = body[:r.limits.MaxBody]
	}
	t.Download = time.Since(firstByte)
	t.Total = time.Since(start)

	out := &Response{
		Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), Proto: resp.Proto,
		Body: body, Truncated: truncated, URL: resp.Request.URL.String(), Redirects: max(redirects, 0), RemoteAddr: remote,
	}
	for key, values := range resp.Header {
		for _, v := range values {
			out.Headers = append(out.Headers, Header{Key: key, Value: v})
		}
	}
	sortHeaders(out.Headers)
	return Result{Response: out, Timings: t}
}

// tracer collects a request's timings. The transport calls it from its own
// goroutines, and a dial it abandons can still report after another
// connection was chosen, so every field is guarded.
type tracer struct {
	mu                                          sync.Mutex
	t                                           Timings
	dnsStart, connectStart, tlsStart, wroteDone time.Time
	remote                                      string
	redirects                                   int
}

func (tr *tracer) do(f func()) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	f()
}

func (tr *tracer) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:     func(httptrace.DNSStartInfo) { tr.do(func() { tr.dnsStart = time.Now() }) },
		DNSDone:      func(httptrace.DNSDoneInfo) { tr.do(func() { tr.t.DNS += since(tr.dnsStart) }) },
		ConnectStart: func(string, string) { tr.do(func() { tr.connectStart = time.Now() }) },
		ConnectDone: func(_, addr string, err error) {
			tr.do(func() {
				if err == nil {
					tr.t.Connect += since(tr.connectStart)
					tr.remote = addr
				}
			})
		},
		TLSHandshakeStart: func() { tr.do(func() { tr.tlsStart = time.Now() }) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { tr.do(func() { tr.t.TLS += since(tr.tlsStart) }) },
		GotConn: func(info httptrace.GotConnInfo) {
			tr.do(func() {
				tr.redirects++
				if info.Conn != nil {
					tr.remote = info.Conn.RemoteAddr().String()
				}
			})
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { tr.do(func() { tr.wroteDone = time.Now() }) },
		GotFirstResponseByte: func() { tr.do(func() { tr.t.FirstByte = since(tr.wroteDone) }) },
	}
}

// snapshot is what the tracer has seen so far.
func (tr *tracer) snapshot() (Timings, string, int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.t, tr.remote, tr.redirects
}

func since(t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}

// build checks a request and makes it an *http.Request.
func (r *Runner) build(ctx context.Context, req Request) (*http.Request, *Failure) {
	invalid := func(msg string) *Failure { return &Failure{Code: "invalid-request", Message: msg} }
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" || strings.ContainsFunc(method, func(c rune) bool { return c <= ' ' || c > '~' || strings.ContainsRune(`()<>@,;:\"/[]?={}`, c) }) {
		return nil, invalid("The method isn't valid. Use one such as GET or POST.")
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	switch {
	case err != nil:
		return nil, invalid("The URL can't be read: " + err.Error())
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, invalid("The URL must start with http:// or https://.")
	case u.Host == "":
		return nil, invalid("The URL has no host, such as api.example.com.")
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, invalid(err.Error())
	}
	for _, h := range req.Headers {
		key := strings.TrimSpace(h.Key)
		if key == "" {
			continue
		}
		if strings.ContainsAny(key+h.Value, "\r\n") {
			return nil, invalid(fmt.Sprintf("The header %q has a line break, which headers can't hold.", key))
		}
		if strings.EqualFold(key, "Host") {
			httpReq.Host = h.Value
			continue
		}
		httpReq.Header.Add(key, h.Value)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", r.userAgent)
	}
	return httpReq, nil
}

// describe turns a transport error into a Failure a person can act on.
func describe(err error, ctx context.Context) *Failure {
	var blocked *egress.BlockedError
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.As(err, &blocked):
		return &Failure{Code: "blocked", Message: fmt.Sprintf("Signalbox doesn't send requests to %s: %s. An admin can allow it.", blocked.Host, blocked.Reason)}
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || os.IsTimeout(err):
		return &Failure{Code: "timeout", Message: "The server didn't answer in time."}
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return &Failure{Code: "dns", Message: fmt.Sprintf("The host %s doesn't exist: DNS has no address for it.", dnsErr.Name)}
		}
		return &Failure{Code: "dns", Message: fmt.Sprintf("DNS couldn't look up %s: %s.", dnsErr.Name, dnsErr.Err)}
	case errors.As(err, &certErr):
		return &Failure{Code: "tls", Message: "The server's TLS certificate isn't valid: " + certErr.Err.Error() + "."}
	case strings.Contains(err.Error(), "tls:"):
		return &Failure{Code: "tls", Message: "The TLS handshake failed: " + innermost(err) + "."}
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return &Failure{Code: "connect", Message: "Couldn't connect: " + innermost(err) + "."}
	}
	return &Failure{Code: "network", Message: "The request failed: " + innermost(err) + "."}
}

// innermost is the most specific part of an error's message.
func innermost(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
}

// sortHeaders orders headers by name, keeping each name's values in order.
func sortHeaders(h []Header) {
	// Insertion sort: header lists are short, and this keeps it stable.
	for i := 1; i < len(h); i++ {
		for j := i; j > 0 && h[j].Key < h[j-1].Key; j-- {
			h[j], h[j-1] = h[j-1], h[j]
		}
	}
}
