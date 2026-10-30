// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

// zipkinSpan is a span in Zipkin's v2 model, which its JSON and protobuf
// encodings share. Times are microseconds.
type zipkinSpan struct {
	TraceID        string             `json:"traceId"`
	ID             string             `json:"id"`
	ParentID       string             `json:"parentId"`
	Kind           string             `json:"kind"`
	Name           string             `json:"name"`
	Timestamp      uint64             `json:"timestamp"`
	Duration       uint64             `json:"duration"`
	LocalEndpoint  *zipkinEndpoint    `json:"localEndpoint"`
	RemoteEndpoint *zipkinEndpoint    `json:"remoteEndpoint"`
	Annotations    []zipkinAnnotation `json:"annotations"`
	Tags           map[string]string  `json:"tags"`
	Shared         bool               `json:"shared"`
}

type zipkinEndpoint struct {
	ServiceName string `json:"serviceName"`
	IPv4        string `json:"ipv4"`
	IPv6        string `json:"ipv6"`
	Port        int32  `json:"port"`
}

type zipkinAnnotation struct {
	Timestamp uint64 `json:"timestamp"`
	Value     string `json:"value"`
}

func decodeZipkinJSON(body []byte) ([]zipkinSpan, error) {
	var spans []zipkinSpan
	if err := json.Unmarshal(body, &spans); err != nil {
		return nil, fmt.Errorf("the body isn't a JSON list of Zipkin v2 spans: %w", err)
	}
	return spans, nil
}

// decodeZipkinProtobuf reads a ListOfSpans from zipkin.proto3, field by
// field: the message is small, and this needs no generated code.
func decodeZipkinProtobuf(body []byte) ([]zipkinSpan, error) {
	var spans []zipkinSpan
	err := eachField(body, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if num != 1 || typ != protowire.BytesType {
			return nil
		}
		s, err := zipkinProtoSpan(v)
		if err != nil {
			return err
		}
		spans = append(spans, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("the body isn't a Zipkin protobuf ListOfSpans: %w", err)
	}
	return spans, nil
}

var zipkinKinds = map[uint64]string{1: "CLIENT", 2: "SERVER", 3: "PRODUCER", 4: "CONSUMER"}

func zipkinProtoSpan(b []byte) (zipkinSpan, error) {
	var s zipkinSpan
	err := eachField(b, func(num protowire.Number, typ protowire.Type, v []byte, n uint64) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			s.TraceID = hex.EncodeToString(v)
		case num == 2 && typ == protowire.BytesType:
			s.ParentID = hex.EncodeToString(v)
		case num == 3 && typ == protowire.BytesType:
			s.ID = hex.EncodeToString(v)
		case num == 4 && typ == protowire.VarintType:
			s.Kind = zipkinKinds[n]
		case num == 5 && typ == protowire.BytesType:
			s.Name = string(v)
		case num == 6 && typ == protowire.Fixed64Type:
			s.Timestamp = n
		case num == 7 && typ == protowire.VarintType:
			s.Duration = n
		case (num == 8 || num == 9) && typ == protowire.BytesType:
			e, err := zipkinProtoEndpoint(v)
			if err != nil {
				return err
			}
			if num == 8 {
				s.LocalEndpoint = &e
			} else {
				s.RemoteEndpoint = &e
			}
		case num == 10 && typ == protowire.BytesType:
			var a zipkinAnnotation
			if err := eachField(v, func(num protowire.Number, typ protowire.Type, v []byte, n uint64) error {
				switch {
				case num == 1 && typ == protowire.Fixed64Type:
					a.Timestamp = n
				case num == 2 && typ == protowire.BytesType:
					a.Value = string(v)
				}
				return nil
			}); err != nil {
				return err
			}
			s.Annotations = append(s.Annotations, a)
		case num == 11 && typ == protowire.BytesType:
			var key, value string
			if err := eachField(v, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
				if typ == protowire.BytesType && num == 1 {
					key = string(v)
				} else if typ == protowire.BytesType && num == 2 {
					value = string(v)
				}
				return nil
			}); err != nil {
				return err
			}
			if s.Tags == nil {
				s.Tags = map[string]string{}
			}
			s.Tags[key] = value
		case num == 13 && typ == protowire.VarintType:
			s.Shared = n != 0
		}
		return nil
	})
	return s, err
}

func zipkinProtoEndpoint(b []byte) (zipkinEndpoint, error) {
	var e zipkinEndpoint
	err := eachField(b, func(num protowire.Number, typ protowire.Type, v []byte, n uint64) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			e.ServiceName = string(v)
		case num == 2 && typ == protowire.BytesType:
			if a, ok := netip.AddrFromSlice(v); ok {
				e.IPv4 = a.String()
			}
		case num == 3 && typ == protowire.BytesType:
			if a, ok := netip.AddrFromSlice(v); ok {
				e.IPv6 = a.String()
			}
		case num == 4 && typ == protowire.VarintType:
			e.Port = int32(n & 0xffff) //nolint:gosec // G115: masked to 16 bits
		}
		return nil
	})
	return e, err
}

// eachField calls f for each field of a protobuf message: its number, wire
// type, and its bytes (for length-delimited fields) or number (for the
// rest).
func eachField(b []byte, f func(num protowire.Number, typ protowire.Type, v []byte, n uint64) error) error {
	for len(b) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(b)
		if tagLen < 0 {
			return protowire.ParseError(tagLen)
		}
		b = b[tagLen:]
		var (
			v    []byte
			n    uint64
			size int
		)
		switch typ {
		case protowire.VarintType:
			n, size = protowire.ConsumeVarint(b)
		case protowire.Fixed64Type:
			n, size = protowire.ConsumeFixed64(b)
		case protowire.Fixed32Type:
			var n32 uint32
			n32, size = protowire.ConsumeFixed32(b)
			n = uint64(n32)
		case protowire.BytesType:
			v, size = protowire.ConsumeBytes(b)
		default:
			size = protowire.ConsumeFieldValue(num, typ, b)
		}
		if size < 0 {
			return protowire.ParseError(size)
		}
		b = b[size:]
		if err := f(num, typ, v, n); err != nil {
			return err
		}
	}
	return nil
}

var zipkinSpanKinds = map[string]telemetry.SpanKind{
	"CLIENT": telemetry.KindClient, "SERVER": telemetry.KindServer,
	"PRODUCER": telemetry.KindProducer, "CONSUMER": telemetry.KindConsumer,
}

// fromZipkin converts Zipkin spans as the OpenTelemetry Collector's Zipkin
// receiver does: a 64-bit trace ID gains eight zero bytes in front, tags
// become attributes, annotations events, and an "error" tag the error
// status.
func fromZipkin(in []zipkinSpan, accept window) (spans []telemetry.Span, rejected int, why string) {
	for i := range in {
		s, err := zipkinToSpan(&in[i], accept)
		if err != nil {
			rejected++
			if why == "" {
				why = err.Error()
			}
			continue
		}
		spans = append(spans, s)
	}
	return spans, rejected, why
}

func zipkinToSpan(z *zipkinSpan, accept window) (telemetry.Span, error) {
	var s telemetry.Span
	traceID := z.TraceID
	if len(traceID) == 16 {
		traceID = strings.Repeat("0", 16) + traceID
	}
	if !hexID(s.TraceID[:], traceID) || s.TraceID.IsZero() {
		return s, fmt.Errorf("%w: a Zipkin span needs a traceId of 16 or 32 hex digits", errInvalidSpan)
	}
	if !hexID(s.SpanID[:], z.ID) || s.SpanID.IsZero() {
		return s, fmt.Errorf("%w: a Zipkin span needs an id of 16 hex digits", errInvalidSpan)
	}
	if z.ParentID != "" && !hexID(s.ParentSpanID[:], z.ParentID) {
		return s, fmt.Errorf("%w: a parentId has 16 hex digits", errInvalidSpan)
	}
	if z.Timestamp == 0 || z.Timestamp > math.MaxInt64/1000 {
		return s, fmt.Errorf("%w: a Zipkin span needs its timestamp", errInvalidSpan)
	}
	s.Start = time.UnixMicro(int64(z.Timestamp)).UTC() //nolint:gosec // G115: bounded above
	if err := accept.check(s.Start); err != nil {
		return s, err
	}
	if z.Duration <= math.MaxInt64/1000 {
		s.Duration = time.Duration(z.Duration) * time.Microsecond //nolint:gosec // G115: bounded above
	}
	s.Name, s.Kind = z.Name, zipkinSpanKinds[strings.ToUpper(z.Kind)]
	s.Attributes = make(map[string]string, len(z.Tags)+4)
	for k, v := range z.Tags {
		s.Attributes[k] = v
	}
	if msg, failed := z.Tags["error"]; failed {
		s.Status, s.StatusMessage = telemetry.StatusError, msg
	}
	if e := z.LocalEndpoint; e != nil {
		s.Service = e.ServiceName
		s.ResourceAttributes = map[string]string{}
		if e.ServiceName != "" {
			s.ResourceAttributes["service.name"] = e.ServiceName
		}
	}
	if e := z.RemoteEndpoint; e != nil {
		if e.ServiceName != "" {
			s.Attributes["peer.service"] = e.ServiceName
			if _, ok := s.Attributes["server.address"]; !ok && s.Kind == telemetry.KindClient {
				s.Attributes["server.address"] = e.ServiceName
			}
		}
		if addr := orString(e.IPv4, e.IPv6); addr != "" {
			s.Attributes["network.peer.address"] = addr
		}
		if e.Port > 0 {
			s.Attributes["network.peer.port"] = strconv.Itoa(int(e.Port))
		}
	}
	for _, a := range z.Annotations {
		ev := telemetry.Event{Name: a.Value}
		if a.Timestamp > 0 && a.Timestamp <= math.MaxInt64/1000 {
			ev.Time = time.UnixMicro(int64(a.Timestamp)).UTC() //nolint:gosec // G115: bounded above
		}
		s.Events = append(s.Events, ev)
	}
	return s, nil
}

func hexID(dst []byte, s string) bool {
	if len(s) != 2*len(dst) {
		return false
	}
	_, err := hex.Decode(dst, []byte(s))
	return err == nil
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// errUnsupported means a body's media type isn't one a protocol takes.
var errUnsupported = errors.New("unsupported media type")
