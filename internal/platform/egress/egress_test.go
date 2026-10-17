// SPDX-License-Identifier: Apache-2.0

package egress_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/egress"
)

func TestCheckAddr(t *testing.T) {
	admin := egress.Policy{
		Deny:      []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")},
		Allow:     []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")},
		Protected: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:5432"), netip.MustParseAddrPort("10.0.0.9:0")},
	}
	tests := []struct {
		policy  egress.Policy
		addr    string
		port    uint16
		blocked bool
	}{
		// Public and private networks are allowed by default.
		{egress.Policy{}, "93.184.215.14", 443, false},
		{egress.Policy{}, "10.1.2.3", 8080, false},
		{egress.Policy{}, "192.168.1.10", 80, false},
		{egress.Policy{}, "172.18.0.4", 5432, false},
		{egress.Policy{}, "fd12:3456::1", 80, false},
		{egress.Policy{}, "2606:4700::1111", 443, false},
		// The built-in blocks.
		{egress.Policy{}, "127.0.0.1", 80, true},
		{egress.Policy{}, "127.255.0.1", 80, true},
		{egress.Policy{}, "0.0.0.0", 80, true},
		{egress.Policy{}, "169.254.169.254", 80, true},
		{egress.Policy{}, "169.254.170.2", 80, true},
		{egress.Policy{}, "100.100.100.200", 80, true},
		{egress.Policy{}, "192.0.0.192", 80, true},
		{egress.Policy{}, "255.255.255.255", 80, true},
		{egress.Policy{}, "224.0.0.1", 80, true},
		{egress.Policy{}, "::1", 80, true},
		{egress.Policy{}, "::", 80, true},
		{egress.Policy{}, "fe80::1", 80, true},
		{egress.Policy{}, "fd00:ec2::254", 80, true},
		// IPv4 addresses hidden in IPv6 ones.
		{egress.Policy{}, "::ffff:169.254.169.254", 80, true},
		{egress.Policy{}, "::ffff:127.0.0.1", 80, true},
		{egress.Policy{}, "::127.0.0.1", 80, true},
		{egress.Policy{}, "64:ff9b::a9fe:a9fe", 80, true}, // NAT64 of 169.254.169.254
		{egress.Policy{}, "64:ff9b:1::7f00:1", 80, true},  // local-use NAT64 of 127.0.0.1
		// Local-use NAT64 of 169.254.169.254 with each RFC 6052 prefix length.
		{egress.Policy{}, "64:ff9b:1:a9fe:a9:fe00::", 80, true},   // /48
		{egress.Policy{}, "64:ff9b:1:a9:fe:a9fe::", 80, true},     // /56
		{egress.Policy{}, "64:ff9b:1:0:a9:fea9:fe00:0", 80, true}, // /64
		{egress.Policy{}, "64:ff9b:1::a9fe:a9fe", 80, true},       // /96
		{egress.Policy{}, "64:ff9b:1:5db8:d7:e00::", 443, true},   // refused even for a public address
		{egress.Policy{}, "2002:a9fe:a9fe::1", 80, true},          // 6to4 of 169.254.169.254
		{egress.Policy{}, "2001:0:1:2::8001:fffe", 80, true},      // Teredo, client 127.0.0.1
		{egress.Policy{}, "64:ff9b::5db8:d70e", 443, false},       // NAT64 of a public address
		{egress.Policy{}, "fe80::1%eth0", 80, true},               // zones are refused
		// Admin rules: Deny before Allow, Allow before the built-in blocks,
		// and Protected before everything.
		{admin, "127.0.0.1", 8080, false},
		{admin, "127.0.0.1", 5432, true},
		{admin, "10.0.0.9", 443, true},
		{admin, "10.20.1.1", 80, true},
		{admin, "10.30.1.1", 80, false},
		{admin, "::ffff:127.0.0.1", 5432, true},
		{admin, "169.254.169.254", 80, true},
	}
	for _, tt := range tests {
		err := tt.policy.CheckAddr(tt.addr, netip.MustParseAddr(tt.addr), tt.port, false)
		if blocked := errors.Is(err, egress.ErrBlocked); blocked != tt.blocked {
			t.Errorf("%s port %d: error = %v, want blocked = %v", tt.addr, tt.port, err, tt.blocked)
		}
	}
}

func TestLocalNAT64AllowedByAnAdminIsStillChecked(t *testing.T) {
	p := egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("64:ff9b:1::/48")}}
	for addr, blocked := range map[string]bool{
		"64:ff9b:1:a9fe:a9:fe00::":       true,  // 169.254.169.254 in the /48 layout
		"64:ff9b:1:a9:fe:a9fe::":         true,  // in the /56 layout
		"64:ff9b:1:0:a9:fea9:fe00:0":     true,  // in the /64 layout
		"64:ff9b:1::a9fe:a9fe":           true,  // in the /96 layout
		"64:ff9b:1:5db8:d7:e00::":        true,  // public in /48, but 0.0.0.0 in /96
		"64:ff9b:1:5db8:d7:e5d:b8d7:e08": false, // public in every layout
	} {
		err := p.CheckAddr(addr, netip.MustParseAddr(addr), 443, false)
		if errors.Is(err, egress.ErrBlocked) != blocked {
			t.Errorf("%s: error = %v, want blocked = %v", addr, err, blocked)
		}
	}
}

func TestHostRules(t *testing.T) {
	p := egress.Policy{
		DenyHosts:  []string{"metadata.google.internal", "*.corp.example"},
		AllowHosts: []string{"localhost", "*.dev.example"},
		Deny:       []netip.Prefix{netip.MustParsePrefix("127.0.0.2/32")},
	}
	for host, want := range map[string]bool{
		"Metadata.Google.Internal.": true,
		"payroll.corp.example":      true,
		"corp.example":              false, // "*." means names under it
		"api.example.com":           false,
	} {
		if _, err := p.CheckHost(host); errors.Is(err, egress.ErrBlocked) != want {
			t.Errorf("CheckHost(%q) = %v, want blocked = %v", host, err, want)
		}
	}
	allowed, _ := p.CheckHost("app.dev.example")
	if !allowed {
		t.Fatal("app.dev.example isn't on the allow list")
	}
	// An allowed host skips the built-in blocks, but not Deny ranges.
	if err := p.CheckAddr("app.dev.example", netip.MustParseAddr("127.0.0.1"), 80, true); err != nil {
		t.Errorf("an allowed host on loopback: %v", err)
	}
	if err := p.CheckAddr("app.dev.example", netip.MustParseAddr("127.0.0.2"), 80, true); !errors.Is(err, egress.ErrBlocked) {
		t.Errorf("an allowed host in a denied range: %v", err)
	}
}

// fakeDNS answers from a table and counts lookups.
type fakeDNS struct {
	answers map[string][]netip.Addr
	lookups atomic.Int32
}

func (f *fakeDNS) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	f.lookups.Add(1)
	if a, ok := f.answers[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func serve(t *testing.T, h http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return srv, u.Port()
}

func TestDialerRefusesLoopbackByDefault(t *testing.T) {
	_, port := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") })
	client := (&egress.Dialer{}).Client(egress.Limits{})
	for _, target := range []string{
		"http://127.0.0.1:" + port + "/",
		"http://[::ffff:127.0.0.1]:" + port + "/",
		"http://localhost:" + port + "/",
	} {
		resp, err := client.Get(target)
		if err == nil {
			resp.Body.Close()
		}
		if !errors.Is(err, egress.ErrBlocked) {
			t.Errorf("GET %s: error = %v, want ErrBlocked", target, err)
		}
	}
}

func TestDialerConnectsOnlyToAddressesItChecked(t *testing.T) {
	_, port := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dns := &fakeDNS{answers: map[string][]netip.Addr{
		// A name that answers with the metadata endpoint first and an
		// allowed address second.
		"api.test": {netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("127.0.0.1")},
		// A name that only points inside.
		"rebind.test": {netip.MustParseAddr("127.0.0.1")},
	}}
	d := &egress.Dialer{Policy: egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}, Resolver: dns}
	client := d.Client(egress.Limits{})

	resp, err := client.Get("http://api.test:" + port + "/")
	if err != nil {
		t.Fatalf("GET api.test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
	if n := dns.lookups.Load(); n != 1 {
		t.Errorf("%d lookups for one request, want 1: the address dialed must be the one checked", n)
	}

	// Without the admin's Allow, the same name is refused, and nothing is
	// dialed.
	strict := &egress.Dialer{Resolver: dns}
	if _, err := strict.DialContext(context.Background(), "tcp", "rebind.test:"+port); !errors.Is(err, egress.ErrBlocked) {
		t.Errorf("rebind.test: error = %v, want ErrBlocked", err)
	}
}

func TestRedirectsAreChecked(t *testing.T) {
	_, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	d := &egress.Dialer{Policy: egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	resp, err := d.Client(egress.Limits{}).Get("http://127.0.0.1:" + port + "/")
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, egress.ErrBlocked) {
		t.Errorf("a redirect to the metadata endpoint: error = %v, want ErrBlocked", err)
	}
}

func TestProxyVariablesAreIgnored(t *testing.T) {
	var proxied atomic.Bool
	_, proxyPort := serve(t, func(w http.ResponseWriter, _ *http.Request) { proxied.Store(true) })
	_, port := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct") })
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:"+proxyPort)
	t.Setenv("http_proxy", "http://127.0.0.1:"+proxyPort)
	d := &egress.Dialer{Policy: egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	resp, err := d.Client(egress.Limits{}).Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if proxied.Load() {
		t.Error("the request went through HTTP_PROXY")
	}
}

func TestLimits(t *testing.T) {
	big := strings.Repeat("x", 2048)
	_, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Header().Set("Content-Length", "2048")
			_, _ = io.WriteString(w, big)
		case "/chunked":
			_, _ = io.WriteString(w, big)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, big)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "small")
		}
	})
	d := &egress.Dialer{Policy: egress.Policy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	client := d.Client(egress.Limits{MaxResponseBytes: 1024, MaxRedirects: 3})
	base := "http://127.0.0.1:" + port

	if _, err := client.Get(base + "/big"); !errors.Is(err, egress.ErrResponseTooLarge) {
		t.Errorf("a declared large body: error = %v", err)
	}
	resp, err := client.Get(base + "/chunked")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, egress.ErrResponseTooLarge) {
		t.Errorf("a streamed large body: error = %v", err)
	}
	resp, err = client.Get(base + "/small")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "small" {
		t.Errorf("a small body = %q, %v", body, err)
	}
	if _, err := client.Get(base + "/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("a redirect loop: error = %v", err)
	}
}

func TestOnlyTCP(t *testing.T) {
	if _, err := (&egress.Dialer{}).DialContext(context.Background(), "udp", "8.8.8.8:53"); err == nil {
		t.Error("dialed UDP")
	}
}
