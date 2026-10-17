// SPDX-License-Identifier: Apache-2.0

package egress_test

import (
	"net/netip"
	"testing"

	"github.com/CemTbi/Signalbox/internal/platform/egress"
)

// BenchmarkCheckAddr measures the policy check each connection pays, with
// an admin policy of typical size.
func BenchmarkCheckAddr(b *testing.B) {
	p := egress.Policy{
		Deny:      []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("192.168.50.0/24")},
		Allow:     []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		Protected: []netip.AddrPort{netip.MustParseAddrPort("172.18.0.2:5432"), netip.MustParseAddrPort("172.18.0.3:9000")},
	}
	addr := netip.MustParseAddr("64:ff9b::5db8:d70e") // passes every rule, including the embedded IPv4
	for b.Loop() {
		if err := p.CheckAddr("api.example.com", addr, 443, false); err != nil {
			b.Fatal(err)
		}
	}
}
