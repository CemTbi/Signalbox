// SPDX-License-Identifier: Apache-2.0

// Package egress decides where Signalbox may send requests on someone's
// behalf: the API client, tests, monitors and the docs' Try it console all
// call addresses that users type. Without a check, a user could make the
// server call a cloud metadata endpoint, its own databases or its own ops
// listener.
//
// A Dialer resolves a hostname once, checks every address it gets back,
// and connects only to addresses that passed. The socket's address is
// checked again just before it connects, so neither a second DNS answer
// (DNS rebinding) nor an address hidden inside an IPv6 address gets past
// the policy. See docs/adr/0016-runner-egress.md.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked matches every *BlockedError with errors.Is.
var ErrBlocked = errors.New("egress: destination blocked")

// BlockedError says which destination the policy refused, and why.
type BlockedError struct {
	Host   string     // the name or address that was asked for
	Addr   netip.Addr // the address that was refused; zero if Host itself was refused
	Reason string
}

func (e *BlockedError) Error() string {
	if e.Addr.IsValid() && e.Addr.String() != e.Host {
		return fmt.Sprintf("egress: %s (%s) is blocked: %s", e.Host, e.Addr, e.Reason)
	}
	return fmt.Sprintf("egress: %s is blocked: %s", e.Host, e.Reason)
}

// Is makes errors.Is(err, ErrBlocked) true.
func (e *BlockedError) Is(target error) bool { return target == ErrBlocked }

// Policy decides which destinations are allowed. The zero Policy allows
// every address except the built-in blocks: private networks are allowed,
// because calling services there is what the runner is for.
//
// Rules apply in this order, and the first that matches decides:
//
//  1. Protected: Signalbox's own services. Always refused.
//  2. DenyHosts and Deny: refused.
//  3. AllowHosts and Allow: allowed.
//  4. The built-in blocks: loopback, link-local (which includes the cloud
//     metadata endpoints), unspecified, multicast and reserved addresses,
//     and the metadata endpoints that live elsewhere. Refused.
//  5. Anything else: allowed.
//
// An IPv6 address that carries an IPv4 address, such as ::ffff:127.0.0.1
// or a NAT64 address, must pass for both.
type Policy struct {
	// Deny and Allow are address ranges set by an admin.
	Deny  []netip.Prefix
	Allow []netip.Prefix
	// DenyHosts and AllowHosts are hostnames set by an admin: an exact name
	// such as "api.example.com", or "*.example.com" for every name under
	// it. An allowed host skips the built-in blocks, whatever it resolves
	// to; Deny ranges and Protected still apply.
	DenyHosts  []string
	AllowHosts []string
	// Protected are the addresses of Signalbox's own PostgreSQL, ClickHouse
	// and other services, which are never reachable from the runner. A
	// port of 0 protects every port of the address.
	Protected []netip.AddrPort
}

// builtin are the ranges refused unless an admin allows them.
var builtin = []struct {
	prefix netip.Prefix
	reason string
}{
	{netip.MustParsePrefix("0.0.0.0/8"), "an unspecified address"},
	{netip.MustParsePrefix("127.0.0.0/8"), "a loopback address"},
	{netip.MustParsePrefix("169.254.0.0/16"), "a link-local address, where cloud metadata endpoints live"},
	{netip.MustParsePrefix("100.100.100.200/32"), "the Alibaba Cloud metadata endpoint"},
	{netip.MustParsePrefix("192.0.0.192/32"), "the Oracle Cloud metadata endpoint"},
	{netip.MustParsePrefix("224.0.0.0/4"), "a multicast address"},
	{netip.MustParsePrefix("240.0.0.0/4"), "a reserved address"},
	{netip.MustParsePrefix("::/128"), "an unspecified address"},
	{netip.MustParsePrefix("::1/128"), "a loopback address"},
	{netip.MustParsePrefix("fe80::/10"), "a link-local address"},
	{netip.MustParsePrefix("fd00:ec2::254/128"), "the AWS metadata endpoint"},
	{netip.MustParsePrefix("ff00::/8"), "a multicast address"},
}

var (
	nat64    = netip.MustParsePrefix("64:ff9b::/96")
	nat64Loc = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToF   = netip.MustParsePrefix("2002::/16")
	teredo   = netip.MustParsePrefix("2001::/32")
	compat   = netip.MustParsePrefix("::/96")
)

// CheckHost reports whether a hostname may be used before it is resolved.
// It returns whether the host is on AllowHosts.
func (p *Policy) CheckHost(host string) (allowed bool, err error) {
	h := normalizeHost(host)
	if matchHost(p.DenyHosts, h) {
		return false, &BlockedError{Host: host, Reason: "the host is on the deny list"}
	}
	return matchHost(p.AllowHosts, h), nil
}

// CheckAddr returns a *BlockedError if the policy refuses addr on port.
// hostAllowed says the name it came from is on AllowHosts.
func (p *Policy) CheckAddr(host string, addr netip.Addr, port uint16, hostAllowed bool) error {
	if addr.Zone() != "" {
		return &BlockedError{Host: host, Addr: addr, Reason: "an address with a zone"}
	}
	addr = addr.Unmap()
	if reason := p.refuse(addr, port, hostAllowed); reason != "" {
		return &BlockedError{Host: host, Addr: addr, Reason: reason}
	}
	if v4, ok := embeddedIPv4(addr); ok {
		if reason := p.refuse(v4, port, hostAllowed); reason != "" {
			return &BlockedError{Host: host, Addr: addr, Reason: "it carries " + v4.String() + ", " + reason}
		}
	}
	return nil
}

// refuse returns why addr is refused, or "".
func (p *Policy) refuse(addr netip.Addr, port uint16, hostAllowed bool) string {
	for _, ap := range p.Protected {
		if ap.Addr().Unmap() == addr && (ap.Port() == 0 || ap.Port() == port) {
			return "one of Signalbox's own services"
		}
	}
	for _, pre := range p.Deny {
		if pre.Contains(addr) {
			return "in a denied range, " + pre.String()
		}
	}
	if hostAllowed {
		return ""
	}
	for _, pre := range p.Allow {
		if pre.Contains(addr) {
			return ""
		}
	}
	for _, b := range builtin {
		if b.prefix.Contains(addr) {
			return b.reason
		}
	}
	if addr.Is4() && addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return "the broadcast address"
	}
	return ""
}

// embeddedIPv4 returns the IPv4 address inside an IPv6 address that
// carries one: IPv4-compatible, NAT64, 6to4 and Teredo addresses. (Mapped
// addresses were unmapped already.)
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	if !a.Is6() {
		return netip.Addr{}, false
	}
	b := a.As16()
	switch {
	case nat64.Contains(a), nat64Loc.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case compat.Contains(a) && !a.IsUnspecified() && a != netip.IPv6Loopback():
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixToF.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	case teredo.Contains(a):
		// The client's address, with every bit flipped.
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}
	return netip.Addr{}, false
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func matchHost(patterns []string, host string) bool {
	for _, p := range patterns {
		p = normalizeHost(strings.TrimSpace(p))
		if suffix, ok := strings.CutPrefix(p, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if p == host {
			return true
		}
	}
	return false
}

// Resolver looks up a hostname's addresses. *net.Resolver is one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer connects only to destinations the policy allows. The zero value
// uses the zero Policy and the system resolver.
type Dialer struct {
	Policy   Policy
	Resolver Resolver
	// Timeout bounds each connection attempt. Default 10 seconds.
	Timeout time.Duration
}

// DialContext resolves the host in address once, and connects to the
// first allowed address that answers. It has the signature of
// http.Transport.DialContext. Only TCP is supported.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("egress: network %q isn't supported", network)
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("egress: port %q: %w", portStr, err)
	}
	hostAllowed, err := d.Policy.CheckHost(host)
	if err != nil {
		return nil, err
	}

	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		resolver := d.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		if addrs, err = resolver.LookupNetIP(ctx, ipNetwork(network), host); err != nil {
			return nil, err
		}
	}

	var allowed []netip.Addr
	var firstBlock error
	for _, a := range addrs {
		if err := d.Policy.CheckAddr(host, a, uint16(port), hostAllowed); err != nil {
			if firstBlock == nil {
				firstBlock = err
			}
			continue
		}
		allowed = append(allowed, a.Unmap())
	}
	if len(allowed) == 0 {
		if firstBlock == nil {
			firstBlock = &BlockedError{Host: host, Reason: "it has no addresses"}
		}
		return nil, firstBlock
	}

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout, Control: d.control(host, hostAllowed)}
	var errs []error
	for _, a := range allowed {
		conn, err := dialer.DialContext(ctx, network, netip.AddrPortFrom(a, uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// control checks the address the socket is about to connect to, the last
// moment before any packet leaves.
func (d *Dialer) control(host string, hostAllowed bool) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return &BlockedError{Host: host, Reason: "an address that can't be checked: " + address}
		}
		return d.Policy.CheckAddr(host, ap.Addr(), ap.Port(), hostAllowed)
	}
}

func ipNetwork(network string) string {
	switch network {
	case "tcp4":
		return "ip4"
	case "tcp6":
		return "ip6"
	}
	return "ip"
}
