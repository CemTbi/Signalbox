// SPDX-License-Identifier: Apache-2.0

package runner_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
	"github.com/CemTbi/Signalbox/internal/platform/egress"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

// loopback lets tests reach their own servers, which the policy refuses
// by default.
var loopback = egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}}

func newRunner(policy egress.Policy, limits runner.Limits) *runner.Runner {
	return runner.New(&egress.Dialer{Policy: policy}, limits, "Signalbox/test")
}

func TestSendsRequestsAndReportsResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Add("X-Echo", r.Method+" "+string(body))
		w.Header().Add("X-Agent", r.UserAgent())
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	res := newRunner(loopback, runner.Limits{}).Run(context.Background(), runner.Request{
		Method: "post", URL: srv.URL + "/orders", Body: []byte("hi"), Headers: []runner.Header{{Key: "Content-Type", Value: "text/plain"}, {Key: "", Value: "ignored"}},
	})
	if res.Failure != nil {
		t.Fatalf("failure = %+v", res.Failure)
	}
	r := res.Response
	if r.Status != 201 || r.StatusText != "Created" || string(r.Body) != `{"ok":true}` || r.Truncated || r.Redirects != 0 {
		t.Errorf("response = %+v", r)
	}
	headers := map[string][]string{}
	for _, h := range r.Headers {
		headers[h.Key] = append(headers[h.Key], h.Value)
	}
	if headers["X-Echo"][0] != "POST hi" || headers["X-Agent"][0] != "Signalbox/test" || len(headers["Set-Cookie"]) != 2 {
		t.Errorf("headers = %v", headers)
	}
	if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || res.Timings.Total <= 0 || res.Timings.Total < res.Timings.Download {
		t.Errorf("remote %q, timings %+v", r.RemoteAddr, res.Timings)
	}
}

func TestLimitsAndRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		}
		if r.URL.Path == "/slow" {
			time.Sleep(300 * time.Millisecond)
		}
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
	}))
	defer srv.Close()
	run := newRunner(loopback, runner.Limits{MaxBody: 10})

	res := run.Run(context.Background(), runner.Request{Method: "GET", URL: srv.URL + "/old"})
	if res.Failure != nil || res.Response.Redirects != 1 || !strings.HasSuffix(res.Response.URL, "/new") ||
		!res.Response.Truncated || len(res.Response.Body) != 10 {
		t.Errorf("redirected = %+v, %+v", res.Response, res.Failure)
	}
	res = run.Run(context.Background(), runner.Request{Method: "GET", URL: srv.URL + "/slow", Timeout: 50 * time.Millisecond})
	if res.Failure == nil || res.Failure.Code != "timeout" {
		t.Errorf("slow = %+v", res.Failure)
	}
}

func TestFailuresSayWhatWentWrong(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	// The default policy refuses loopback.
	if f := newRunner(egress.Policy{}, runner.Limits{}).Run(ctx, runner.Request{Method: "GET", URL: srv.URL}).Failure; f == nil || f.Code != "blocked" ||
		!strings.Contains(f.Message, "loopback") {
		t.Errorf("loopback = %+v", f)
	}
	noSuchHost := runner.New(&egress.Dialer{Resolver: nxdomain{}}, runner.Limits{}, "t")
	if f := noSuchHost.Run(ctx, runner.Request{Method: "GET", URL: "https://nowhere.example/"}).Failure; f == nil || f.Code != "dns" ||
		!strings.Contains(f.Message, "nowhere.example") {
		t.Errorf("unknown host = %+v", f)
	}
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	if f := newRunner(loopback, runner.Limits{}).Run(ctx, runner.Request{Method: "GET", URL: tlsSrv.URL}).Failure; f == nil || f.Code != "tls" {
		t.Errorf("self-signed certificate = %+v", f)
	}
	for _, req := range []runner.Request{
		{Method: "GET", URL: "ftp://example.com"},
		{Method: "GET", URL: "https://"},
		{Method: "G ET", URL: "https://example.com"},
		{Method: "GET", URL: "https://example.com", Headers: []runner.Header{{Key: "X", Value: "a\r\nInjected: 1"}}},
	} {
		if f := newRunner(loopback, runner.Limits{}).Run(ctx, req).Failure; f == nil || f.Code != "invalid-request" {
			t.Errorf("%+v: %+v", req, f)
		}
	}
}

type nxdomain struct{}

func (nxdomain) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestCountsRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	reg := metrics.NewRegistry()
	m := runner.NewMetrics(reg)
	open := newRunner(loopback, runner.Limits{})
	open.UseMetrics(m)
	// The default policy refuses loopback.
	closed := newRunner(egress.Policy{}, runner.Limits{})
	closed.UseMetrics(m)
	ctx := context.Background()
	open.Run(ctx, runner.Request{Method: "GET", URL: srv.URL})
	open.Run(ctx, runner.Request{Method: "GET", URL: srv.URL})
	closed.Run(ctx, runner.Request{Method: "GET", URL: srv.URL})
	m.Observe("agent", runner.Result{Failure: &runner.Failure{Code: "agent-offline"}}, time.Second)

	var b bytes.Buffer
	reg.WriteTo(&b)
	for _, want := range []string{
		`signalbox_runner_requests_total{where="agent",outcome="agent-offline"} 1`,
		`signalbox_runner_requests_total{where="server",outcome="blocked"} 1`,
		`signalbox_runner_requests_total{where="server",outcome="response"} 2`,
		`signalbox_runner_request_duration_seconds_count{where="server"} 3`,
		`signalbox_runner_request_duration_seconds_bucket{where="agent",le="1"} 1`,
	} {
		if !strings.Contains(b.String(), want+"\n") {
			t.Errorf("lacks %s:\n%s", want, b.String())
		}
	}
}
