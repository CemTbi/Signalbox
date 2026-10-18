// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestParseDestinations(t *testing.T) {
	prefixes, hosts, err := parseDestinations("SIGNALBOX_EGRESS_ALLOW", " 10.0.0.0/8, 192.168.1.7 ,fd00::/8, API.example.com, *.internal.example.com,")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.7/32"), netip.MustParsePrefix("fd00::/8")}
	if !slices.Equal(prefixes, want) || !slices.Equal(hosts, []string{"api.example.com", "*.internal.example.com"}) {
		t.Errorf("parsed %v %v", prefixes, hosts)
	}
	for _, bad := range []string{"10.0.0.0/33", "exa mple.com", "http://example.com", "*.*.example.com"} {
		if _, _, err := parseDestinations("SIGNALBOX_EGRESS_DENY", bad); err == nil || !strings.Contains(err.Error(), "SIGNALBOX_EGRESS_DENY") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestEgressPolicyProtectsTheDatabase(t *testing.T) {
	p, err := egressPolicy(context.Background(), "postgres://u:p@10.1.2.3:6543/db", "", "169.254.0.0/16", net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Protected, netip.MustParseAddrPort("10.1.2.3:6543")) || len(p.Deny) != 1 {
		t.Errorf("policy = %+v", p)
	}
	// A hostname is denied by name, and its addresses are protected.
	p, err = egressPolicy(context.Background(), "postgres://u:p@localhost:5432/db", "", "", net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.DenyHosts, "localhost") || len(p.Protected) == 0 {
		t.Errorf("policy = %+v", p)
	}
	// A Unix socket needs no protection.
	if p, err := egressPolicy(context.Background(), "postgres:///db?host=/var/run/postgresql", "", "", net.DefaultResolver); err != nil || len(p.Protected) != 0 {
		t.Errorf("socket: %+v, %v", p, err)
	}
}
