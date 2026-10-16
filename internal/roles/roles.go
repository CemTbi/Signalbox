// SPDX-License-Identifier: Apache-2.0

// Package roles names the parts of signalbox-server that can run on their own.
//
// A single machine runs every role in one process. On Kubernetes each role
// runs as its own deployment and scales on its own metric. See
// docs/adr/0001-single-binary-with-roles.md.
package roles

import (
	"fmt"
	"strings"
)

// Name identifies a role.
type Name string

const (
	API       Name = "api"       // web app, REST and WebSocket API
	Mock      Name = "mock"      // hosted mock servers
	Runner    Name = "runner"    // sends requests for the client, tests and monitors
	Scheduler Name = "scheduler" // starts monitors and scheduled runs on time
	Worker    Name = "worker"    // lint runs, docs builds, codegen, notifications
	Ingest    Name = "ingest"    // receives OTLP and Zipkin spans
)

// All lists every role in start order.
var All = []Name{API, Mock, Runner, Scheduler, Worker, Ingest}

// available lists the roles this version can run. The rest are planned; the
// roadmap in docs/product/requirements-1.0.md says when each one lands.
var available = map[Name]bool{API: true}

// Available reports whether this version can run the role.
func Available(n Name) bool { return available[n] }

// Parse reads a --roles value: "all", or a comma-separated list such as
// "api,mock". "all" means every role this version can run. The result is in
// start order with duplicates removed.
func Parse(value string) ([]Name, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "all" {
		var names []Name
		for _, n := range All {
			if available[n] {
				names = append(names, n)
			}
		}
		return names, nil
	}

	wanted := map[Name]bool{}
	for _, part := range strings.Split(value, ",") {
		n := Name(strings.TrimSpace(part))
		if n == "" {
			continue
		}
		if !known(n) {
			return nil, fmt.Errorf("unknown role %q: use all, or a list of %s", n, joinNames(All))
		}
		if !available[n] {
			return nil, fmt.Errorf("role %q is planned but not available in this version", n)
		}
		wanted[n] = true
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("no roles given: use all, or a list of %s", joinNames(All))
	}

	var names []Name
	for _, n := range All {
		if wanted[n] {
			names = append(names, n)
		}
	}
	return names, nil
}

func known(n Name) bool {
	for _, k := range All {
		if k == n {
			return true
		}
	}
	return false
}

func joinNames(names []Name) string {
	s := make([]string, len(names))
	for i, n := range names {
		s[i] = string(n)
	}
	return strings.Join(s, ", ")
}
