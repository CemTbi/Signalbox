// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CemTbi/Signalbox/internal/platform/egress"
)

// hostPattern is a hostname, or *.example.com for every name under one.
var hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// parseDestinations reads a comma-separated list of address ranges,
// addresses and hostnames, as SIGNALBOX_EGRESS_ALLOW and _DENY take.
func parseDestinations(setting, value string) (prefixes []netip.Prefix, hosts []string, err error) {
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		switch {
		case item == "":
		case strings.Contains(item, "/"):
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %q isn't an address range such as 10.0.0.0/8", setting, item)
			}
			prefixes = append(prefixes, p.Masked())
		default:
			if a, err := netip.ParseAddr(item); err == nil {
				prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
			if !hostPattern.MatchString(item) {
				return nil, nil, fmt.Errorf("%s: %q isn't an address, an address range or a hostname such as api.example.com or *.example.com", setting, item)
			}
			hosts = append(hosts, item)
		}
	}
	return prefixes, hosts, nil
}

// egressPolicy is where the runner may send requests: the admin's ranges
// and hosts, with Signalbox's own database protected by address and by
// name. See docs/adr/0016-runner-egress.md.
func egressPolicy(ctx context.Context, databaseURL, allow, deny string, resolver *net.Resolver) (egress.Policy, error) {
	var p egress.Policy
	var err error
	if p.Allow, p.AllowHosts, err = parseDestinations("SIGNALBOX_EGRESS_ALLOW", allow); err != nil {
		return p, err
	}
	if p.Deny, p.DenyHosts, err = parseDestinations("SIGNALBOX_EGRESS_DENY", deny); err != nil {
		return p, err
	}
	db, err := pgconn.ParseConfig(databaseURL)
	if err != nil {
		return p, fmt.Errorf("read the database URL: %w", err)
	}
	targets := []struct {
		host string
		port uint16
	}{{db.Host, db.Port}}
	for _, f := range db.Fallbacks {
		targets = append(targets, struct {
			host string
			port uint16
		}{f.Host, f.Port})
	}
	for _, t := range targets {
		// A Unix socket isn't reachable over the network.
		if t.host == "" || strings.HasPrefix(t.host, "/") {
			continue
		}
		if a, err := netip.ParseAddr(t.host); err == nil {
			p.Protected = append(p.Protected, netip.AddrPortFrom(a.Unmap(), t.port))
			continue
		}
		p.DenyHosts = append(p.DenyHosts, strings.ToLower(t.host))
		lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
		addrs, err := resolver.LookupNetIP(lookup, "ip", t.host)
		cancel()
		if err != nil {
			return p, fmt.Errorf("look up the database host %s, to keep the runner from reaching it: %w", t.host, err)
		}
		for _, a := range addrs {
			p.Protected = append(p.Protected, netip.AddrPortFrom(a.Unmap(), t.port))
		}
	}
	return p, nil
}
