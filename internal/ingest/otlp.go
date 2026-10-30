// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

// decodeOTLPProtobuf reads an ExportTraceServiceRequest. TracesData has the
// same fields and numbers, so it reads one without the collector package,
// which would bring gRPC along.
func decodeOTLPProtobuf(body []byte) (*tracepb.TracesData, error) {
	var td tracepb.TracesData
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, &td); err != nil {
		return nil, fmt.Errorf("the body isn't an OTLP protobuf message: %w", err)
	}
	return &td, nil
}

// OTLP/JSON differs from protobuf's own JSON mapping: trace and span IDs
// are hex, not base64, and enums are numbers. These types read it as the
// OTLP specification defines, into the protobuf types.
type (
	jsonTraces struct {
		ResourceSpans []jsonResourceSpans `json:"resourceSpans"`
	}
	jsonResourceSpans struct {
		Resource   *jsonResource    `json:"resource"`
		ScopeSpans []jsonScopeSpans `json:"scopeSpans"`
		SchemaURL  string           `json:"schemaUrl"`
	}
	jsonResource struct {
		Attributes []jsonKeyValue `json:"attributes"`
	}
	jsonScopeSpans struct {
		Scope *struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"scope"`
		Spans []jsonSpan `json:"spans"`
	}
	jsonSpan struct {
		TraceID           string         `json:"traceId"`
		SpanID            string         `json:"spanId"`
		ParentSpanID      string         `json:"parentSpanId"`
		TraceState        string         `json:"traceState"`
		Name              string         `json:"name"`
		Kind              jsonEnum       `json:"kind"`
		StartTimeUnixNano jsonUint64     `json:"startTimeUnixNano"`
		EndTimeUnixNano   jsonUint64     `json:"endTimeUnixNano"`
		Attributes        []jsonKeyValue `json:"attributes"`
		Events            []struct {
			TimeUnixNano jsonUint64     `json:"timeUnixNano"`
			Name         string         `json:"name"`
			Attributes   []jsonKeyValue `json:"attributes"`
		} `json:"events"`
		Links []struct {
			TraceID    string         `json:"traceId"`
			SpanID     string         `json:"spanId"`
			TraceState string         `json:"traceState"`
			Attributes []jsonKeyValue `json:"attributes"`
		} `json:"links"`
		Status *struct {
			Code    jsonEnum `json:"code"`
			Message string   `json:"message"`
		} `json:"status"`
	}
	jsonKeyValue struct {
		Key   string     `json:"key"`
		Value *jsonValue `json:"value"`
	}
	jsonValue struct {
		StringValue *string    `json:"stringValue"`
		BoolValue   *bool      `json:"boolValue"`
		IntValue    *jsonInt64 `json:"intValue"`
		DoubleValue *float64   `json:"doubleValue"`
		BytesValue  *string    `json:"bytesValue"`
		ArrayValue  *struct {
			Values []jsonValue `json:"values"`
		} `json:"arrayValue"`
		KvlistValue *struct {
			Values []jsonKeyValue `json:"values"`
		} `json:"kvlistValue"`
	}
)

// jsonUint64 reads a 64-bit number sent as a string, as OTLP/JSON does, or
// as a number.
type jsonUint64 uint64

func (n *jsonUint64) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseUint(strings.Trim(string(b), `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("%s isn't a whole number", b)
	}
	*n = jsonUint64(v)
	return nil
}

type jsonInt64 int64

func (n *jsonInt64) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64)
	if err != nil {
		return fmt.Errorf("%s isn't a whole number", b)
	}
	*n = jsonInt64(v)
	return nil
}

// jsonEnum reads an enum as its number, as OTLP/JSON sends it, or as its
// name, such as SPAN_KIND_SERVER, which some clients send.
type jsonEnum int32

var enumNames = map[string]int32{
	"SPAN_KIND_UNSPECIFIED": 0, "SPAN_KIND_INTERNAL": 1, "SPAN_KIND_SERVER": 2, "SPAN_KIND_CLIENT": 3,
	"SPAN_KIND_PRODUCER": 4, "SPAN_KIND_CONSUMER": 5,
	"STATUS_CODE_UNSET": 0, "STATUS_CODE_OK": 1, "STATUS_CODE_ERROR": 2,
}

func (e *jsonEnum) UnmarshalJSON(b []byte) error {
	if n, err := strconv.ParseInt(string(b), 10, 32); err == nil {
		*e = jsonEnum(n)
		return nil
	}
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		if n, ok := enumNames[name]; ok {
			*e = jsonEnum(n)
			return nil
		}
	}
	return fmt.Errorf("%s isn't a known enum value", b)
}

// decodeOTLPJSON reads an ExportTraceServiceRequest in OTLP/JSON.
func decodeOTLPJSON(body []byte) (*tracepb.TracesData, error) {
	var in jsonTraces
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("the body isn't OTLP/JSON: %w", err)
	}
	td := &tracepb.TracesData{}
	for _, rs := range in.ResourceSpans {
		out := &tracepb.ResourceSpans{SchemaUrl: rs.SchemaURL}
		if rs.Resource != nil {
			out.Resource = &resourcepb.Resource{Attributes: keyValues(rs.Resource.Attributes)}
		}
		for _, ss := range rs.ScopeSpans {
			scope := &tracepb.ScopeSpans{}
			if ss.Scope != nil {
				scope.Scope = &commonpb.InstrumentationScope{Name: ss.Scope.Name, Version: ss.Scope.Version}
			}
			for _, s := range ss.Spans {
				span := &tracepb.Span{
					TraceId: hexBytes(s.TraceID), SpanId: hexBytes(s.SpanID), ParentSpanId: hexBytes(s.ParentSpanID),
					TraceState: s.TraceState, Name: s.Name, Kind: tracepb.Span_SpanKind(s.Kind),
					StartTimeUnixNano: uint64(s.StartTimeUnixNano), EndTimeUnixNano: uint64(s.EndTimeUnixNano),
					Attributes: keyValues(s.Attributes),
				}
				for _, e := range s.Events {
					span.Events = append(span.Events, &tracepb.Span_Event{TimeUnixNano: uint64(e.TimeUnixNano), Name: e.Name, Attributes: keyValues(e.Attributes)})
				}
				for _, l := range s.Links {
					span.Links = append(span.Links, &tracepb.Span_Link{TraceId: hexBytes(l.TraceID), SpanId: hexBytes(l.SpanID),
						TraceState: l.TraceState, Attributes: keyValues(l.Attributes)})
				}
				if s.Status != nil {
					span.Status = &tracepb.Status{Code: tracepb.Status_StatusCode(s.Status.Code), Message: s.Status.Message}
				}
				scope.Spans = append(scope.Spans, span)
			}
			out.ScopeSpans = append(out.ScopeSpans, scope)
		}
		td.ResourceSpans = append(td.ResourceSpans, out)
	}
	return td, nil
}

// hexBytes decodes a hex ID. A malformed one decodes to nothing, which the
// span's checks then refuse.
func hexBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func keyValues(in []jsonKeyValue) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(in))
	for _, kv := range in {
		out = append(out, &commonpb.KeyValue{Key: kv.Key, Value: kv.Value.proto()})
	}
	return out
}

func (v *jsonValue) proto() *commonpb.AnyValue {
	switch {
	case v == nil:
		return nil
	case v.StringValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: *v.StringValue}}
	case v.BoolValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: *v.BoolValue}}
	case v.IntValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(*v.IntValue)}}
	case v.DoubleValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: *v.DoubleValue}}
	case v.BytesValue != nil:
		b, _ := base64.StdEncoding.DecodeString(*v.BytesValue)
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: b}}
	case v.ArrayValue != nil:
		arr := &commonpb.ArrayValue{}
		for i := range v.ArrayValue.Values {
			arr.Values = append(arr.Values, v.ArrayValue.Values[i].proto())
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: arr}}
	case v.KvlistValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: keyValues(v.KvlistValue.Values)}}}
	}
	return nil
}

// errInvalidSpan explains why a span was refused.
var errInvalidSpan = errors.New("invalid span")

// fromOTLP converts the spans of a request. Spans that can't be stored,
// such as those without valid IDs, are counted and described, not
// returned.
func fromOTLP(td *tracepb.TracesData, accept window) (spans []telemetry.Span, rejected int, why string) {
	for _, rs := range td.GetResourceSpans() {
		resource := attributes(rs.GetResource().GetAttributes())
		service := resource["service.name"]
		for _, ss := range rs.GetScopeSpans() {
			for _, ps := range ss.GetSpans() {
				s, err := otlpSpan(ps, accept)
				if err != nil {
					rejected++
					if why == "" {
						why = err.Error()
					}
					continue
				}
				s.Service = service
				s.ResourceAttributes = cloneMap(resource)
				s.ScopeName, s.ScopeVersion = ss.GetScope().GetName(), ss.GetScope().GetVersion()
				spans = append(spans, s)
			}
		}
	}
	return spans, rejected, why
}

func otlpSpan(ps *tracepb.Span, accept window) (telemetry.Span, error) {
	var s telemetry.Span
	if !copyID(s.TraceID[:], ps.GetTraceId()) || s.TraceID.IsZero() {
		return s, fmt.Errorf("%w: a span needs a trace ID of 16 bytes that aren't all zero", errInvalidSpan)
	}
	if !copyID(s.SpanID[:], ps.GetSpanId()) || s.SpanID.IsZero() {
		return s, fmt.Errorf("%w: a span needs a span ID of 8 bytes that aren't all zero", errInvalidSpan)
	}
	if len(ps.GetParentSpanId()) > 0 && !copyID(s.ParentSpanID[:], ps.GetParentSpanId()) {
		return s, fmt.Errorf("%w: a parent span ID has 8 bytes", errInvalidSpan)
	}
	start, end := ps.GetStartTimeUnixNano(), ps.GetEndTimeUnixNano()
	if start == 0 || start > math.MaxInt64 {
		return s, fmt.Errorf("%w: a span needs its start time", errInvalidSpan)
	}
	s.Start = time.Unix(0, int64(start)).UTC()
	if err := accept.check(s.Start); err != nil {
		return s, err
	}
	if end > start && end-start <= math.MaxInt64 {
		s.Duration = time.Duration(end - start) //nolint:gosec // G115: at most MaxInt64, checked above
	}
	s.TraceState, s.Name = ps.GetTraceState(), ps.GetName()
	if k := ps.GetKind(); k >= 0 && k <= tracepb.Span_SPAN_KIND_CONSUMER {
		s.Kind = telemetry.SpanKind(k)
	}
	if c := ps.GetStatus().GetCode(); c >= 0 && c <= tracepb.Status_STATUS_CODE_ERROR {
		s.Status = telemetry.StatusCode(c)
	}
	s.StatusMessage = ps.GetStatus().GetMessage()
	s.Attributes = attributes(ps.GetAttributes())
	for _, e := range ps.GetEvents() {
		ev := telemetry.Event{Name: e.GetName(), Attributes: attributes(e.GetAttributes())}
		if t := e.GetTimeUnixNano(); t > 0 && t <= math.MaxInt64 {
			ev.Time = time.Unix(0, int64(t)).UTC()
		}
		s.Events = append(s.Events, ev)
	}
	for _, l := range ps.GetLinks() {
		var link telemetry.Link
		if !copyID(link.TraceID[:], l.GetTraceId()) || !copyID(link.SpanID[:], l.GetSpanId()) {
			continue // a link nobody can follow
		}
		link.TraceState, link.Attributes = l.GetTraceState(), attributes(l.GetAttributes())
		s.Links = append(s.Links, link)
	}
	return s, nil
}

func copyID(dst, src []byte) bool {
	if len(src) != len(dst) {
		return false
	}
	copy(dst, src)
	return true
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// attributes flattens OTLP attributes into text, as Signalbox stores them:
// arrays and maps as JSON, bytes as base64.
func attributes(kvs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv.GetKey() == "" {
			continue
		}
		out[kv.GetKey()] = valueText(kv.GetValue())
	}
	return out
}

func valueText(v *commonpb.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	case *commonpb.AnyValue_ArrayValue, *commonpb.AnyValue_KvlistValue:
		var b bytes.Buffer
		_ = json.NewEncoder(&b).Encode(valueTree(v))
		return strings.TrimSuffix(b.String(), "\n")
	}
	return ""
}

// valueTree turns a value into plain Go values for JSON.
func valueTree(v *commonpb.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		if math.IsInf(x.DoubleValue, 0) || math.IsNaN(x.DoubleValue) {
			return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
		}
		return x.DoubleValue
	case *commonpb.AnyValue_BytesValue:
		return x.BytesValue // base64 in JSON
	case *commonpb.AnyValue_ArrayValue:
		out := make([]any, 0, len(x.ArrayValue.GetValues()))
		for _, e := range x.ArrayValue.GetValues() {
			out = append(out, valueTree(e))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		out := make(map[string]any, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			out[kv.GetKey()] = valueTree(kv.GetValue())
		}
		return out
	}
	return nil
}
