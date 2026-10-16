// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 192.0.2.7, ::1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"direct client", "203.0.113.5:4711", nil, "203.0.113.5"},
		{"untrusted peer can't claim an address", "203.0.113.5:4711", []string{"198.51.100.1"}, "203.0.113.5"},
		{"trusted proxy", "10.1.2.3:80", []string{"198.51.100.1"}, "198.51.100.1"},
		{"chain of trusted proxies", "10.1.2.3:80", []string{"198.51.100.1, 192.0.2.7, 10.9.9.9"}, "198.51.100.1"},
		{"spoofed entry left of the client", "10.1.2.3:80", []string{"1.1.1.1, 198.51.100.1"}, "198.51.100.1"},
		{"several headers", "10.1.2.3:80", []string{"198.51.100.1", "10.4.4.4"}, "198.51.100.1"},
		{"only proxies", "10.1.2.3:80", []string{"10.4.4.4"}, "10.1.2.3"},
		{"garbage stops the walk", "10.1.2.3:80", []string{"198.51.100.1, not-an-ip"}, "10.1.2.3"},
		{"IPv6 proxy", "[::1]:80", []string{"2001:db8::7"}, "2001:db8::7"},
		{"IPv4-mapped IPv6 peer", "[::ffff:203.0.113.5]:4711", nil, "203.0.113.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.forwarded {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r, trusted).String(); got != tt.want {
				t.Errorf("clientIP = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	for _, s := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0.0/8, nope"} {
		if _, err := ParseTrustedProxies(s); err == nil {
			t.Errorf("ParseTrustedProxies(%q) accepted it", s)
		}
	}
	if got, err := ParseTrustedProxies(" "); err != nil || len(got) != 0 {
		t.Errorf("empty list = %v, %v", got, err)
	}
}

func TestOriginDropsDefaultPorts(t *testing.T) {
	for in, want := range map[string]string{
		"https://Signalbox.Example.com:443": "https://signalbox.example.com",
		"http://localhost:80":               "http://localhost",
		"http://localhost:8080":             "http://localhost:8080",
		"https://example.com:8443":          "https://example.com:8443",
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := origin(u); got != want {
			t.Errorf("origin(%s) = %s, want %s", in, got, want)
		}
	}
}
