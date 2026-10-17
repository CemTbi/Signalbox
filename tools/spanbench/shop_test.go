// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

func TestGeneratedTracesHoldTogether(t *testing.T) {
	gen := newGenerator(42, env)
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var spans, serverSpans, serverErrors, traces int
	services := map[string]bool{}
	for i := range 5000 {
		trace := gen.trace(start.Add(time.Duration(i) * time.Millisecond))
		traces++
		spans += len(trace)
		byID := map[telemetry.SpanID]telemetry.Span{}
		roots := 0
		for _, s := range trace {
			if s.TraceID != trace[0].TraceID {
				t.Fatalf("trace %d mixes trace IDs", i)
			}
			if _, dup := byID[s.SpanID]; dup {
				t.Fatalf("trace %d repeats span ID %s", i, s.SpanID)
			}
			byID[s.SpanID] = s
			if s.ParentSpanID.IsZero() {
				roots++
			}
			services[s.Service] = true
			if s.Kind == telemetry.KindServer {
				serverSpans++
				if s.Status == telemetry.StatusError {
					serverErrors++
				}
			}
		}
		if roots != 1 {
			t.Fatalf("trace %d has %d root spans", i, roots)
		}
		for _, s := range trace {
			if s.ParentSpanID.IsZero() {
				continue
			}
			p, ok := byID[s.ParentSpanID]
			if !ok {
				t.Fatalf("span %s's parent isn't in trace %d", s.SpanID, i)
			}
			if s.Start.Before(p.Start) {
				t.Fatalf("span %s starts before its parent", s.SpanID)
			}
			// A message's consumer may finish after its producer.
			if s.Kind != telemetry.KindConsumer && s.Start.Add(s.Duration).After(p.Start.Add(p.Duration)) {
				t.Fatalf("%s %q ends after its parent %q", s.Service, s.Name, p.Name)
			}
		}
	}
	perTrace := float64(spans) / float64(traces)
	errorShare := float64(serverErrors) / float64(serverSpans)
	t.Logf("%.1f spans per trace, %.2f%% of server spans failed, %d services", perTrace, 100*errorShare, len(services))
	if perTrace < 6 || perTrace > 30 {
		t.Errorf("%.1f spans per trace; a shop request makes about 10 to 20", perTrace)
	}
	if errorShare < 0.001 || errorShare > 0.03 {
		t.Errorf("%.2f%% of server spans failed; expect a fraction of a percent", 100*errorShare)
	}
	if len(services) != 12 {
		t.Errorf("%d services sent spans, want all 12", len(services))
	}
}

func TestGeneratorIsDeterministic(t *testing.T) {
	start := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	a, b := newGenerator(7, env).trace(start), newGenerator(7, env).trace(start)
	if len(a) != len(b) || a[0].TraceID != b[0].TraceID || a[len(a)-1].Duration != b[len(b)-1].Duration {
		t.Error("the same seed made different traces")
	}
}
