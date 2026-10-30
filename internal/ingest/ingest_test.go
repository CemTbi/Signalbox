// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
	"github.com/CemTbi/Signalbox/internal/platform/metrics"
)

const goodKey = "sbx_ing_good"

var env = uuid.MustParse("0192f6a0-0000-7000-8000-000000000001")

type fakeKeys struct{}

func (fakeKeys) Authenticate(_ context.Context, key string) (telemetry.Sender, error) {
	if key == goodKey {
		return telemetry.Sender{EnvironmentID: env}, nil
	}
	return telemetry.Sender{}, telemetry.ErrUnknownKey
}

type fakeSpans struct {
	mu    sync.Mutex
	spans []telemetry.Span
	err   error
	// block, when set, holds writes until it closes or their context ends.
	block   chan struct{}
	blocked int
}

func (f *fakeSpans) Write(ctx context.Context, spans []telemetry.Span) error {
	if f.block != nil {
		f.mu.Lock()
		f.blocked++
		f.mu.Unlock()
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.spans = append(f.spans, spans...)
	return nil
}

func (f *fakeSpans) waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blocked
}

func (f *fakeSpans) take() []telemetry.Span {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.spans
	f.spans = nil
	return out
}

func newServer(t *testing.T, opts Options) (*Server, *fakeSpans, *metrics.Registry) {
	t.Helper()
	spans := &fakeSpans{}
	reg := metrics.NewRegistry()
	s := New(fakeKeys{}, spans, slog.New(slog.DiscardHandler), opts, reg)
	s.now = func() time.Time { return now }
	return s, spans, reg
}

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func post(t *testing.T, h http.Handler, path, contentType, encoding string, body []byte, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if encoding != "" {
		r.Header.Set("Content-Encoding", encoding)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func str(v string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
}

// traces is an OTLP request with a server span, a client span and a span
// whose ID is all zeros.
func traces() *tracepb.TracesData {
	start := uint64(now.Add(-time.Minute).UnixNano())
	span := func(id byte, kind tracepb.Span_SpanKind, name string, attrs ...*commonpb.KeyValue) *tracepb.Span {
		return &tracepb.Span{
			TraceId: bytes.Repeat([]byte{0xab}, 16), SpanId: bytes.Repeat([]byte{id}, 8), Name: name, Kind: kind,
			StartTimeUnixNano: start, EndTimeUnixNano: start + uint64(25*time.Millisecond), Attributes: attrs,
		}
	}
	server := span(1, tracepb.Span_SPAN_KIND_SERVER, "GET",
		&commonpb.KeyValue{Key: "http.request.method", Value: str("GET")},
		&commonpb.KeyValue{Key: "http.route", Value: str("/orders/{id}")},
		&commonpb.KeyValue{Key: "http.response.status_code", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 500}}},
		&commonpb.KeyValue{Key: "http.request.header.authorization", Value: str("Bearer secret")},
		&commonpb.KeyValue{Key: "app.tags", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{
			Values: []*commonpb.AnyValue{str("a"), {Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}}}}}})
	server.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "boom"}
	server.Events = []*tracepb.Span_Event{{TimeUnixNano: start + 1000, Name: "exception"}}
	client := span(2, tracepb.Span_SPAN_KIND_CLIENT, "SELECT shop")
	client.ParentSpanId = server.SpanId
	bad := span(0, tracepb.Span_SPAN_KIND_INTERNAL, "zeros")
	return &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{Key: "service.name", Value: str("orders")}}},
		ScopeSpans: []*tracepb.ScopeSpans{{Scope: &commonpb.InstrumentationScope{Name: "otelhttp", Version: "0.60"}, Spans: []*tracepb.Span{server, client, bad}}},
	}}}
}

func TestOTLPProtobuf(t *testing.T) {
	s, spans, reg := newServer(t, Options{})
	body, err := proto.Marshal(traces())
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(body)
	_ = zw.Close()

	w := post(t, s.OTLPHandler(), "/v1/traces", "application/x-protobuf", "gzip", gz.Bytes(), goodKey)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("%d %s: %q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	// Partial success: field 1, holding rejected_spans 1 and a message.
	partial := w.Body.Bytes()
	if len(partial) == 0 || !strings.Contains(string(partial), "trace ID") && !strings.Contains(string(partial), "span ID") {
		t.Errorf("response = %x", partial)
	}

	got := spans.take()
	if len(got) != 2 {
		t.Fatalf("stored %d spans", len(got))
	}
	srv := got[0]
	if srv.EnvironmentID != env || srv.Service != "orders" || srv.ScopeName != "otelhttp" || srv.Kind != telemetry.KindServer ||
		srv.Status != telemetry.StatusError || srv.StatusMessage != "boom" || srv.Duration != 25*time.Millisecond ||
		srv.HTTPStatusCode != 500 || srv.Operation != "GET /orders/{id}" || len(srv.Events) != 1 {
		t.Errorf("server span = %+v", srv)
	}
	if srv.Attributes["http.request.header.authorization"] != telemetry.Redacted || srv.Attributes["app.tags"] != `["a",true]` {
		t.Errorf("attributes = %v", srv.Attributes)
	}
	if got[1].ParentSpanID != srv.SpanID || got[1].Operation != "SELECT shop" {
		t.Errorf("client span = %+v", got[1])
	}
	var b bytes.Buffer
	reg.WriteTo(&b)
	for _, want := range []string{
		`signalbox_ingest_requests_total{protocol="otlp_http",outcome="partial"} 1`,
		`signalbox_ingest_spans_accepted_total{protocol="otlp_http"} 2`,
		`signalbox_ingest_spans_refused_total{protocol="otlp_http",reason="invalid"} 1`,
	} {
		if !strings.Contains(b.String(), want+"\n") {
			t.Errorf("lacks %s", want)
		}
	}
}

func TestOTLPJSON(t *testing.T) {
	s, spans, _ := newServer(t, Options{})
	start := now.Add(-time.Minute).UnixNano()
	body := fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
		"scopeSpans":[{"scope":{"name":"manual"},"spans":[
		{"traceId":"%s","spanId":"0102030405060708","name":"checkout","kind":"SPAN_KIND_SERVER",
		 "startTimeUnixNano":"%d","endTimeUnixNano":%d,
		 "attributes":[{"key":"n","value":{"intValue":"42"}},{"key":"d","value":{"doubleValue":1.5}},{"key":"m","value":{"kvlistValue":{"values":[{"key":"k","value":{"stringValue":"v"}}]}}}],
		 "status":{"code":2}}]}]}]}`, strings.Repeat("ab", 16), start, start+1_000_000)
	var zs bytes.Buffer
	zw, _ := zstd.NewWriter(&zs)
	_, _ = zw.Write([]byte(body))
	_ = zw.Close()
	w := post(t, s.OTLPHandler(), "/v1/traces", "application/json; charset=utf-8", "zstd", zs.Bytes(), goodKey)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	got := spans.take()
	if len(got) != 1 {
		t.Fatalf("stored %d", len(got))
	}
	sp := got[0]
	if hex.EncodeToString(sp.SpanID[:]) != "0102030405060708" || sp.Kind != telemetry.KindServer || sp.Status != telemetry.StatusError ||
		sp.Duration != time.Millisecond || sp.Attributes["n"] != "42" || sp.Attributes["d"] != "1.5" || sp.Attributes["m"] != `{"k":"v"}` {
		t.Errorf("span = %+v", sp)
	}
}

func TestOTLPErrors(t *testing.T) {
	s, spans, _ := newServer(t, Options{MaxBodyBytes: 1 << 10})
	h := s.OTLPHandler()
	body, _ := proto.Marshal(traces())
	big := bytes.Repeat([]byte("x"), 2<<10)
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, _ = zw.Write(bytes.Repeat([]byte{0}, 1<<20)) // 1 MiB of zeros in about 1 KiB
	_ = zw.Close()
	var zbomb bytes.Buffer
	zsw, _ := zstd.NewWriter(&zbomb)
	_, _ = zsw.Write(bytes.Repeat([]byte{0}, 1<<20))
	_ = zsw.Close()

	for _, tc := range []struct {
		name, contentType, encoding, key string
		body                             []byte
		status                           int
	}{
		{"no key", "application/x-protobuf", "", "", body, http.StatusUnauthorized},
		{"unknown key", "application/x-protobuf", "", "sbx_ing_nope", body, http.StatusUnauthorized},
		{"wrong media type", "text/plain", "", goodKey, body, http.StatusUnsupportedMediaType},
		{"unknown encoding", "application/x-protobuf", "br", goodKey, body, http.StatusUnsupportedMediaType},
		{"not protobuf", "application/x-protobuf", "", goodKey, []byte{0xff, 0xff, 0xff}, http.StatusBadRequest},
		{"not JSON", "application/json", "", goodKey, []byte("{"), http.StatusBadRequest},
		{"too large", "application/x-protobuf", "", goodKey, big, http.StatusRequestEntityTooLarge},
		{"decompression bomb", "application/x-protobuf", "gzip", goodKey, bomb.Bytes(), http.StatusRequestEntityTooLarge},
		{"zstd decompression bomb", "application/x-protobuf", "zstd", goodKey, zbomb.Bytes(), http.StatusRequestEntityTooLarge},
		{"not gzip", "application/x-protobuf", "gzip", goodKey, body, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := post(t, h, "/v1/traces", tc.contentType, tc.encoding, tc.body, tc.key)
			if w.Code != tc.status {
				t.Errorf("status %d, want %d: %q", w.Code, tc.status, w.Body.String())
			}
		})
	}
	// Errors in JSON are a google.rpc.Status in JSON.
	w := post(t, h, "/v1/traces", "application/json", "", []byte("{}"), "")
	var st struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Code != 16 || !strings.Contains(st.Message, "ingest key") {
		t.Errorf("status = %+v, %v", st, err)
	}
	// In protobuf, too: code 16, then the message.
	w = post(t, h, "/v1/traces", "application/x-protobuf", "", body, "")
	num, typ, n := protowire.ConsumeTag(w.Body.Bytes())
	code, _ := protowire.ConsumeVarint(w.Body.Bytes()[n:])
	if num != 1 || typ != protowire.VarintType || code != 16 {
		t.Errorf("protobuf status = %x", w.Body.Bytes())
	}
	if w := httptest.NewRecorder(); true {
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/traces", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET = %d", w.Code)
		}
	}
	if len(spans.take()) != 0 {
		t.Error("an error stored spans")
	}
}

func TestOldAndFutureSpansAreRefused(t *testing.T) {
	s, spans, _ := newServer(t, Options{RetentionDays: 7})
	td := traces()
	all := td.ResourceSpans[0].ScopeSpans[0].Spans
	all[0].StartTimeUnixNano = uint64(now.AddDate(0, 0, -8).UnixNano())
	all[1].StartTimeUnixNano = uint64(now.Add(2 * time.Hour).UnixNano())
	body, _ := proto.Marshal(td)
	w := post(t, s.OTLPHandler(), "/v1/traces", "application/x-protobuf", "", body, goodKey)
	if w.Code != http.StatusOK || len(spans.take()) != 0 {
		t.Errorf("%d: %q", w.Code, w.Body.String())
	}
}

func TestThrottledAndBusy(t *testing.T) {
	s, _, _ := newServer(t, Options{SpansPerSecond: 1})
	body, _ := proto.Marshal(traces())
	h := s.OTLPHandler()
	// Two spans fit the burst of two; the next request waits.
	if w := post(t, h, "/v1/traces", "application/x-protobuf", "", body, goodKey); w.Code != http.StatusOK {
		t.Fatalf("first: %d", w.Code)
	}
	w := post(t, h, "/v1/traces", "application/x-protobuf", "", body, goodKey)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("second: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}

	s, spans, _ := newServer(t, Options{})
	spans.err = telemetry.ErrBusy
	w = post(t, s.ZipkinHandler(), "/api/v2/spans", "application/json", "", []byte(zipkinJSON()), goodKey)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Errorf("busy: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	spans.err = io.ErrUnexpectedEOF
	w = post(t, s.ZipkinHandler(), "/api/v2/spans", "application/json", "", []byte(zipkinJSON()), goodKey)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Errorf("failed: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func zipkinJSON() string {
	ts := now.Add(-time.Minute).UnixMicro()
	return fmt.Sprintf(`[
	  {"traceId":"463ac35c9f6413ad","id":"a2fb4a1d1a96d312","kind":"CLIENT","name":"get /api","timestamp":%d,"duration":2500,
	   "localEndpoint":{"serviceName":"frontend","ipv4":"192.168.99.1"},
	   "remoteEndpoint":{"serviceName":"backend","ipv4":"172.19.0.2","port":8080},
	   "annotations":[{"timestamp":%d,"value":"ws"}],
	   "tags":{"http.method":"GET","http.path":"/api","error":"timeout"}},
	  {"traceId":"463ac35c9f6413ad48485a3953bb6124","id":"0000000000000000","name":"bad","timestamp":%d}
	]`, ts, ts+10, ts)
}

func TestZipkinJSON(t *testing.T) {
	s, spans, _ := newServer(t, Options{})
	w := post(t, s.ZipkinHandler(), "/api/v2/spans", "", "", []byte(zipkinJSON()), goodKey)
	if w.Code != http.StatusAccepted || w.Header().Get("X-Signalbox-Rejected-Spans") != "1" {
		t.Fatalf("%d %v: %s", w.Code, w.Header(), w.Body.String())
	}
	got := spans.take()
	if len(got) != 1 {
		t.Fatalf("stored %d", len(got))
	}
	sp := got[0]
	if hex.EncodeToString(sp.TraceID[:]) != "0000000000000000463ac35c9f6413ad" || sp.Service != "frontend" || sp.Kind != telemetry.KindClient ||
		sp.Duration != 2500*time.Microsecond || sp.Status != telemetry.StatusError || sp.StatusMessage != "timeout" ||
		sp.HTTPMethod != "GET" || sp.ServerAddress != "backend" || sp.Attributes["network.peer.port"] != "8080" || len(sp.Events) != 1 {
		t.Errorf("span = %+v", sp)
	}
	if w := post(t, s.ZipkinHandler(), "/api/v2/spans", "", "", []byte(zipkinJSON()), "sbx_ing_nope"); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown key: %d", w.Code)
	}
}

func TestZipkinProtobuf(t *testing.T) {
	s, spans, _ := newServer(t, Options{})
	ts := uint64(now.Add(-time.Minute).UnixMicro())
	field := func(b []byte, num protowire.Number, v []byte) []byte {
		b = protowire.AppendTag(b, num, protowire.BytesType)
		return protowire.AppendBytes(b, v)
	}
	var endpoint []byte
	endpoint = field(endpoint, 1, []byte("checkout"))
	endpoint = field(endpoint, 2, []byte{10, 0, 0, 7})
	var tag []byte
	tag = field(tag, 1, []byte("http.route"))
	tag = field(tag, 2, []byte("/pay"))
	var span []byte
	span = field(span, 1, bytes.Repeat([]byte{0x11}, 16))
	span = field(span, 3, bytes.Repeat([]byte{0x22}, 8))
	span = protowire.AppendTag(span, 4, protowire.VarintType)
	span = protowire.AppendVarint(span, 2) // SERVER
	span = field(span, 5, []byte("post /pay"))
	span = protowire.AppendTag(span, 6, protowire.Fixed64Type)
	span = protowire.AppendFixed64(span, ts)
	span = protowire.AppendTag(span, 7, protowire.VarintType)
	span = protowire.AppendVarint(span, 1500)
	span = field(span, 8, endpoint)
	span = field(span, 11, tag)
	span = protowire.AppendTag(span, 99, protowire.VarintType) // unknown fields are skipped
	span = protowire.AppendVarint(span, 1)
	list := field(nil, 1, span)

	w := post(t, s.ZipkinHandler(), "/api/v2/spans", "application/x-protobuf", "", list, goodKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	got := spans.take()
	if len(got) != 1 || got[0].Service != "checkout" || got[0].Kind != telemetry.KindServer || got[0].HTTPRoute != "/pay" ||
		got[0].Duration != 1500*time.Microsecond || got[0].Name != "post /pay" {
		t.Errorf("spans = %+v", got)
	}
	if w := post(t, s.ZipkinHandler(), "/api/v2/spans", "application/x-protobuf", "", []byte{0x0a, 0xff}, goodKey); w.Code != http.StatusBadRequest {
		t.Errorf("truncated protobuf: %d", w.Code)
	}
}
