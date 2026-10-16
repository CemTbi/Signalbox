// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP finds the address a request came from. Behind a reverse proxy the
// connection comes from the proxy, so X-Forwarded-For is read, but only when
// the connection comes from a trusted proxy: anyone else could write any
// address into the header. The rightmost address that isn't a trusted proxy
// is the client.
func clientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	remote := parseAddr(r.RemoteAddr)
	if !remote.IsValid() || !isTrusted(remote, trusted) {
		return remote
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr := parseAddr(strings.TrimSpace(hops[i]))
		if !addr.IsValid() {
			break // a malformed entry ends what can be believed
		}
		if !isTrusted(addr, trusted) {
			return addr
		}
	}
	return remote
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseAddr reads "ip", "ip:port" or "[ipv6]:port".
func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// ParseTrustedProxies reads a comma-separated list of addresses and CIDR
// ranges, such as "10.0.0.0/8, 192.0.2.7".
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, err
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}
