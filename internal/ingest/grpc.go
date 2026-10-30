// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // OTLP exporters may compress with gzip
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// traceService is OTLP's gRPC service for traces.
const traceService = "opentelemetry.proto.collector.trace.v1.TraceService"

// GRPCServer serves OTLP over gRPC: TraceService/Export. The method is
// registered by hand, with a codec that hands it the raw message, which
// it reads as TracesData like OTLP/HTTP does. The generated service
// lives in a package that also brings grpc-gateway along.
func (s *Server) GRPCServer() *grpc.Server {
	srv := grpc.NewServer(
		grpc.ForceServerCodecV2(rawCodec{}),
		grpc.MaxRecvMsgSize(int(s.opts.MaxBodyBytes)),
		grpc.MaxConcurrentStreams(1000),
		grpc.ConnectionTimeout(10*time.Second),
		// Exporters keep connections open; these let idle ones go and keep
		// broken ones from lingering.
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 5 * time.Minute, Time: 2 * time.Minute, Timeout: 20 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
	)
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: traceService,
		HandlerType: (*any)(nil),
		Methods:     []grpc.MethodDesc{{MethodName: "Export", Handler: s.export}},
		Metadata:    "opentelemetry/proto/collector/trace/v1/trace_service.proto",
	}, s)
	return srv
}

// export handles one Export call.
func (s *Server) export(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
	const protocol = "otlp_grpc"
	var body []byte
	if err := decode(&body); err != nil {
		s.requests.Inc(protocol, "invalid")
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	var authorization, host string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			authorization = v[0]
		}
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		host, _, _ = net.SplitHostPort(p.Addr.String())
	}
	sender, res, ok := s.authenticate(ctx, protocol, authorization, host)
	if !ok {
		return nil, grpcError(res)
	}
	td, err := decodeOTLPProtobuf(body)
	if err != nil {
		s.requests.Inc(protocol, "invalid")
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	spans, rejected, why := fromOTLP(td, s.window())
	res = s.accept(ctx, protocol, sender, spans, rejected, why)
	if res.status != http.StatusOK {
		return nil, grpcError(res)
	}
	return rawMessage(exportResponse(res)), nil
}

// grpcError turns a refusal into the status OTLP/gRPC defines. Refusals to
// retry carry RetryInfo: exporters retry RESOURCE_EXHAUSTED only with it.
func grpcError(res result) error {
	var code codes.Code
	switch res.status {
	case http.StatusUnauthorized:
		code = codes.Unauthenticated
	case http.StatusTooManyRequests:
		code = codes.ResourceExhausted
	case http.StatusServiceUnavailable:
		code = codes.Unavailable
	default:
		code = codes.InvalidArgument
	}
	st := status.New(code, res.message)
	if res.retryAfter > 0 {
		if withRetry, err := st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(res.retryAfter)}); err == nil {
			st = withRetry
		}
	}
	return st.Err()
}

// rawMessage is a message already in protobuf's wire format.
type rawMessage []byte

// rawCodec passes messages through as bytes, in both directions: requests
// arrive as *[]byte for the handler to decode, and answers leave as
// rawMessage. It is named proto, as gRPC clients expect.
type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	m, ok := v.(rawMessage)
	if !ok {
		return nil, errors.New("ingest: only raw messages are sent")
	}
	return mem.BufferSlice{mem.SliceBuffer(m)}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	b, ok := v.(*[]byte)
	if !ok {
		return errors.New("ingest: messages are read as bytes")
	}
	*b = data.Materialize()
	return nil
}
