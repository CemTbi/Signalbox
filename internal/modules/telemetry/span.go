// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// TraceID identifies a trace, as in W3C Trace Context.
type TraceID [16]byte

// SpanID identifies a span within its trace.
type SpanID [8]byte

// ParseTraceID reads the 32 lowercase hex digits of a trace ID.
func ParseTraceID(s string) (TraceID, error) {
	var id TraceID
	if len(s) != 2*len(id) {
		return id, errors.New("a trace ID has 32 hex digits")
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, errors.New("a trace ID has 32 hex digits")
	}
	return id, nil
}

func (id TraceID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether the ID is all zeros, which W3C Trace Context
// forbids.
func (id TraceID) IsZero() bool { return id == TraceID{} }

func (id SpanID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether the ID is all zeros: the parent of a root span.
func (id SpanID) IsZero() bool { return id == SpanID{} }

// SpanKind says what a span describes, as in OpenTelemetry.
type SpanKind uint8

// Span kinds, with OpenTelemetry's numbering.
const (
	KindUnspecified SpanKind = iota
	KindInternal
	KindServer
	KindClient
	KindProducer
	KindConsumer
)

// StatusCode is a span's outcome, as in OpenTelemetry.
type StatusCode uint8

// Status codes, with OpenTelemetry's numbering.
const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

// Span is one operation in a trace, as Signalbox stores it. It follows the
// OpenTelemetry data model, with the attributes that Signalbox filters and
// groups by most often copied into their own fields.
type Span struct {
	// EnvironmentID is the project environment the span was sent to.
	EnvironmentID uuid.UUID
	TraceID       TraceID
	SpanID        SpanID
	// ParentSpanID is zero for a root span.
	ParentSpanID SpanID
	TraceState   string
	Start        time.Time
	Duration     time.Duration
	// Service is the resource's service.name.
	Service       string
	Name          string
	Kind          SpanKind
	Status        StatusCode
	StatusMessage string

	// Copies of attributes from the HTTP and database semantic
	// conventions, which Signalbox filters and groups by. Attributes holds
	// them too; the store keeps each value once.
	HTTPMethod     string // http.request.method
	HTTPRoute      string // http.route
	HTTPStatusCode uint16 // http.response.status_code
	ServerAddress  string // server.address: the host a client span calls
	DBSystem       string // db.system.name

	ScopeName          string
	ScopeVersion       string
	ResourceAttributes map[string]string
	Attributes         map[string]string
	Events             []Event
	Links              []Link
}

// Event is something that happened during a span, such as an exception.
type Event struct {
	Time       time.Time
	Name       string
	Attributes map[string]string
}

// Link points from a span to a span in another trace.
type Link struct {
	TraceID    TraceID
	SpanID     SpanID
	TraceState string
	Attributes map[string]string
}
