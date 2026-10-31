// SPDX-License-Identifier: Apache-2.0

package client

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

// withTraceparent puts the request in a W3C trace, so an API that is
// traced continues it and the response can link to it. A request that
// sets its own traceparent keeps it, and joins that trace; one that sets
// an empty one opts out, and the empty header isn't sent. It returns the
// trace ID in hex, or "" when the request isn't traced.
func withTraceparent(req *runner.Request) string {
	for i, h := range req.Headers {
		if !strings.EqualFold(strings.TrimSpace(h.Key), "traceparent") {
			continue
		}
		value := strings.TrimSpace(h.Value)
		if value == "" {
			req.Headers = append(req.Headers[:i:i], req.Headers[i+1:]...)
			return ""
		}
		return traceIDOf(value)
	}
	var trace [16]byte
	var span [8]byte
	_, _ = rand.Read(trace[:]) // crypto/rand never fails
	_, _ = rand.Read(span[:])
	id := hex.EncodeToString(trace[:])
	// Version 00, sampled, so the API records the trace.
	req.Headers = append(req.Headers, runner.Header{Key: "traceparent", Value: "00-" + id + "-" + hex.EncodeToString(span[:]) + "-01"})
	return id
}

// traceIDOf reads the trace ID from a traceparent value, or "" when it
// isn't one: version-traceid-parentid-flags, lowercase hex, and a trace
// ID that isn't all zeros.
func traceIDOf(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) < 4 || len(parts[0]) != 2 || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return ""
	}
	id := parts[1]
	if _, err := hex.DecodeString(id); err != nil || id != strings.ToLower(id) || id == strings.Repeat("0", 32) {
		return ""
	}
	return id
}
