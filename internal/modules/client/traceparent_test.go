// SPDX-License-Identifier: Apache-2.0

package client

import (
	"regexp"
	"testing"

	"github.com/CemTbi/Signalbox/internal/modules/runner"
)

func TestWithTraceparent(t *testing.T) {
	// A request without one gets a new, sampled trace.
	req := runner.Request{Headers: []runner.Header{{Key: "Accept", Value: "*/*"}}}
	id := withTraceparent(&req)
	if len(req.Headers) != 2 || req.Headers[1].Key != "traceparent" ||
		!regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(req.Headers[1].Value) || req.Headers[1].Value[3:35] != id {
		t.Errorf("headers = %v, id %q", req.Headers, id)
	}
	if other := withTraceparent(&runner.Request{}); other == id {
		t.Error("two requests share a trace")
	}

	// One that sets its own joins that trace.
	own := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req = runner.Request{Headers: []runner.Header{{Key: "TraceParent", Value: own}}}
	if id := withTraceparent(&req); id != "4bf92f3577b34da6a3ce929d0e0e4736" || len(req.Headers) != 1 || req.Headers[0].Value != own {
		t.Errorf("own = %q, headers %v", id, req.Headers)
	}
	// An empty one opts out and isn't sent.
	req = runner.Request{Headers: []runner.Header{{Key: "A", Value: "1"}, {Key: "traceparent", Value: " "}, {Key: "B", Value: "2"}}}
	if id := withTraceparent(&req); id != "" || len(req.Headers) != 2 || req.Headers[1].Key != "B" {
		t.Errorf("opted out = %q, headers %v", id, req.Headers)
	}
	// One that isn't a traceparent is sent as it is, but links nowhere.
	for _, bad := range []string{"nonsense", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01", "00-" + "00000000000000000000000000000000" + "-00f067aa0ba902b7-01"} {
		req = runner.Request{Headers: []runner.Header{{Key: "traceparent", Value: bad}}}
		if id := withTraceparent(&req); id != "" || req.Headers[0].Value != bad {
			t.Errorf("%q = %q", bad, id)
		}
	}
}
