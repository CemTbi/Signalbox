// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"net"
	"testing"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

// grpcClient serves s over gRPC and returns OTLP's own generated client
// for it, as exporters use.
func grpcClient(t *testing.T, s *Server) coltracepb.TraceServiceClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := s.GRPCServer()
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return coltracepb.NewTraceServiceClient(conn)
}

func withKey(t *testing.T, key string) context.Context {
	return metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+key)
}

func TestOTLPGRPC(t *testing.T) {
	s, spans, _ := newServer(t, Options{})
	client := grpcClient(t, s)
	req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: traces().ResourceSpans}

	// Compressed with gzip, as exporters often are.
	resp, err := client.Export(withKey(t, goodKey), req, grpc.UseCompressor(gzip.Name))
	if err != nil {
		t.Fatal(err)
	}
	if ps := resp.GetPartialSuccess(); ps.GetRejectedSpans() != 1 || ps.GetErrorMessage() == "" {
		t.Errorf("partial success = %+v", ps)
	}
	got := spans.take()
	if len(got) != 2 || got[0].Service != "orders" || got[0].EnvironmentID != env || got[0].Operation != "GET /orders/{id}" {
		t.Errorf("spans = %+v", got)
	}

	_, err = client.Export(withKey(t, "sbx_ing_nope"), req)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("unknown key: %v", err)
	}
	_, err = client.Export(context.Background(), req)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("no key: %v", err)
	}
}

func TestOTLPGRPCRetries(t *testing.T) {
	s, _, _ := newServer(t, Options{SpansPerSecond: 1})
	client := grpcClient(t, s)
	req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: traces().ResourceSpans}
	if _, err := client.Export(withKey(t, goodKey), req); err != nil {
		t.Fatal(err)
	}
	// Over the rate: RESOURCE_EXHAUSTED with RetryInfo, which exporters
	// need before they retry it.
	_, err := client.Export(withKey(t, goodKey), req)
	st := status.Convert(err)
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("throttled: %v", err)
	}
	var retry *errdetails.RetryInfo
	for _, d := range st.Details() {
		if r, ok := d.(*errdetails.RetryInfo); ok {
			retry = r
		}
	}
	if retry == nil || retry.GetRetryDelay().AsDuration() <= 0 {
		t.Errorf("no RetryInfo: %v", st.Details())
	}

	s, spans, _ := newServer(t, Options{})
	spans.err = telemetry.ErrBusy
	if _, err := grpcClient(t, s).Export(withKey(t, goodKey), req); status.Code(err) != codes.Unavailable {
		t.Errorf("busy: %v", err)
	}
}

func TestOTLPGRPCRefusesLargeMessages(t *testing.T) {
	s, _, _ := newServer(t, Options{MaxBodyBytes: 1 << 10})
	td := traces()
	big := td.ResourceSpans[0].ScopeSpans[0].Spans[0]
	big.Name = string(make([]byte, 2<<10))
	_, err := grpcClient(t, s).Export(withKey(t, goodKey), &coltracepb.ExportTraceServiceRequest{ResourceSpans: td.ResourceSpans})
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("a message over the limit: %v", err)
	}
}
