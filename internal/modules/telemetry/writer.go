// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/column"

	"github.com/CemTbi/Signalbox/internal/platform/clickhouse"
)

const insertSpans = `INSERT INTO spans (
    environment_id, timestamp, trace_id, span_id, parent_span_id, trace_state,
    service_name, span_name, operation, span_kind, duration_ns, status_code, status_message,
    http_method, http_route, http_status_code, server_address, db_system,
    scope_name, scope_version, resource, span_attributes,
    ` + "`events.timestamp`, `events.name`, `events.attributes`," + `
    ` + "`links.trace_id`, `links.span_id`, `links.trace_state`, `links.attributes`" + `
)`

// WriteSpans inserts spans in one batch. ClickHouse works best with few,
// large inserts: send thousands of spans at a time, about once a second.
func WriteSpans(ctx context.Context, conn clickhouse.Conn, spans []Span) error {
	if len(spans) == 0 {
		return nil
	}
	batch, err := conn.PrepareBatch(ctx, insertSpans)
	if err != nil {
		return fmt.Errorf("prepare the span insert: %w", err)
	}
	defer func() { _ = batch.Abort() }() // a no-op once sent
	for i := range spans {
		s := &spans[i]
		resource, err := json.Marshal(s.ResourceAttributes) // sorts the keys
		if err != nil {
			return fmt.Errorf("encode the resource of span %s: %w", s.SpanID, err)
		}
		eventTimes, eventNames, eventAttrs := splitEvents(s.Events)
		linkTraces, linkSpans, linkStates, linkAttrs := splitLinks(s.Links)
		operation := s.Operation
		if operation == "" {
			operation = s.Name
		}
		err = batch.Append(
			s.EnvironmentID, s.Start, s.TraceID[:], s.SpanID[:], s.ParentSpanID[:], s.TraceState,
			s.Service, s.Name, operation, s.Kind.String(), durationNS(s.Duration), s.Status.String(), s.StatusMessage,
			s.HTTPMethod, s.HTTPRoute, s.HTTPStatusCode, s.ServerAddress, s.DBSystem,
			s.ScopeName, s.ScopeVersion, string(resource), sorted(storedAttributes(s)),
			eventTimes, eventNames, eventAttrs,
			linkTraces, linkSpans, linkStates, linkAttrs,
		)
		if err != nil {
			return fmt.Errorf("add span %s of trace %s to the insert: %w", s.SpanID, s.TraceID, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("insert %d spans: %w", len(spans), err)
	}
	return nil
}

func splitEvents(events []Event) ([]time.Time, []string, []*sortedMap) {
	times := make([]time.Time, len(events))
	names := make([]string, len(events))
	attrs := make([]*sortedMap, len(events))
	for i, e := range events {
		times[i], names[i], attrs[i] = e.Time, e.Name, sorted(e.Attributes)
	}
	return times, names, attrs
}

func splitLinks(links []Link) ([][]byte, [][]byte, []string, []*sortedMap) {
	traces := make([][]byte, len(links))
	spans := make([][]byte, len(links))
	states := make([]string, len(links))
	attrs := make([]*sortedMap, len(links))
	for i, l := range links {
		traces[i], spans[i], states[i], attrs[i] = l.TraceID[:], l.SpanID[:], l.TraceState, sorted(l.Attributes)
	}
	return traces, spans, states, attrs
}

// durationNS converts a duration for a UInt64 column. A negative one, which
// a broken clock can produce, counts as zero.
func durationNS(d time.Duration) uint64 {
	if d < 0 {
		return 0
	}
	return uint64(d) //nolint:gosec // G115: not negative, checked above.
}

// fromNS converts a UInt64 column back to a duration, capped at the
// longest one Go can hold.
func fromNS(ns uint64) time.Duration {
	return time.Duration(min(ns, math.MaxInt64)) //nolint:gosec // G115: capped at MaxInt64.
}

// promoted lists the attributes with a field of their own, which reads the
// attribute's value from a span.
var promoted = map[string]func(*Span) string{
	"http.request.method":       func(s *Span) string { return s.HTTPMethod },
	"http.route":                func(s *Span) string { return s.HTTPRoute },
	"http.response.status_code": func(s *Span) string { return statusText(s.HTTPStatusCode) },
	"server.address":            func(s *Span) string { return s.ServerAddress },
	"db.system.name":            func(s *Span) string { return s.DBSystem },
}

func statusText(code uint16) string {
	if code == 0 {
		return ""
	}
	return strconv.Itoa(int(code))
}

// storedAttributes leaves out the attributes whose field holds the same
// value, so they are stored once. restoreAttributes puts them back.
func storedAttributes(s *Span) map[string]string {
	out := make(map[string]string, len(s.Attributes))
	for k, v := range s.Attributes {
		if field, ok := promoted[k]; ok && v != "" && field(s) == v {
			continue
		}
		out[k] = v
	}
	return out
}

// restoreAttributes adds the attributes kept only in their fields.
func restoreAttributes(s *Span) {
	if s.Attributes == nil {
		s.Attributes = map[string]string{}
	}
	for k, field := range promoted {
		if v := field(s); v != "" {
			if _, ok := s.Attributes[k]; !ok {
				s.Attributes[k] = v
			}
		}
	}
}

// sortedMap sends a map to ClickHouse with its keys in order. Go randomizes
// the order of map iteration, and two equal maps in different orders are
// different bytes, which compress worse and hash differently.
type sortedMap struct {
	keys, values []string
}

func sorted(m map[string]string) *sortedMap {
	out := &sortedMap{keys: make([]string, 0, len(m)), values: make([]string, 0, len(m))}
	for k := range m {
		out.keys = append(out.keys, k)
	}
	slices.Sort(out.keys)
	for _, k := range out.keys {
		out.values = append(out.values, m[k])
	}
	return out
}

// Put and Iterator make sortedMap a column.IterableOrderedMap.
func (m *sortedMap) Put(key, value any) {
	m.keys = append(m.keys, key.(string))
	m.values = append(m.values, value.(string))
}

func (m *sortedMap) Iterator() column.MapIterator { return &sortedMapIterator{m: m, i: -1} }

type sortedMapIterator struct {
	m *sortedMap
	i int
}

func (it *sortedMapIterator) Next() bool {
	it.i++
	return it.i < len(it.m.keys)
}

func (it *sortedMapIterator) Key() any   { return it.m.keys[it.i] }
func (it *sortedMapIterator) Value() any { return it.m.values[it.i] }
