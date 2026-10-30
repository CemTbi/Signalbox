// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

// Media types the protocols use.
const (
	mediaProtobuf = "application/x-protobuf"
	mediaJSON     = "application/json"
)

// OTLPHandler serves OTLP/HTTP's POST /v1/traces, in protobuf or JSON.
// Answers use the request's encoding: an ExportTraceServiceResponse, with
// partial success when some spans were refused, or a google.rpc.Status
// that explains an error.
func (s *Server) OTLPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		// Answers echo what senders sent, such as why a span was refused:
		// browsers must take them as the type they say they are.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !onlyPost(w, r) {
			return
		}
		res := s.ingest(r, "otlp_http", decodeOTLP)
		asJSON := mediaTypeOf(r) == mediaJSON
		if res.status != http.StatusOK {
			writeOTLPStatus(w, res, asJSON)
			return
		}
		writeOTLPResponse(w, res, asJSON)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Send traces with POST /v1/traces.", http.StatusNotFound)
	})
	return mux
}

// onlyPost answers 405 to anything but POST.
func onlyPost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "Send spans with POST.", http.StatusMethodNotAllowed)
	return false
}

func decodeOTLP(body []byte, mediaType string, accept window) ([]telemetry.Span, int, string, error) {
	switch mediaType {
	case mediaProtobuf:
		td, err := decodeOTLPProtobuf(body)
		if err != nil {
			return nil, 0, "", err
		}
		spans, rejected, why := fromOTLP(td, accept)
		return spans, rejected, why, nil
	case mediaJSON:
		td, err := decodeOTLPJSON(body)
		if err != nil {
			return nil, 0, "", err
		}
		spans, rejected, why := fromOTLP(td, accept)
		return spans, rejected, why, nil
	}
	return nil, 0, "", fmt.Errorf("%w: send OTLP as %s or %s", errUnsupported, mediaProtobuf, mediaJSON)
}

func mediaTypeOf(r *http.Request) string {
	if t := r.Header.Get("Content-Type"); t != "" {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			return mt
		}
	}
	return ""
}

// gRPC status codes that OTLP/HTTP errors carry.
const (
	codeInvalidArgument   = 3
	codeResourceExhausted = 8
	codeUnavailable       = 14
	codeUnauthenticated   = 16
)

func grpcCode(status int) uint64 {
	switch status {
	case http.StatusUnauthorized:
		return codeUnauthenticated
	case http.StatusTooManyRequests:
		return codeResourceExhausted
	case http.StatusServiceUnavailable:
		return codeUnavailable
	}
	return codeInvalidArgument
}

func writeOTLPStatus(w http.ResponseWriter, res result, asJSON bool) {
	retryAfter(w, res.retryAfter)
	code := grpcCode(res.status)
	if asJSON {
		w.Header().Set("Content-Type", mediaJSON)
		w.WriteHeader(res.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": res.message})
		return
	}
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, code)
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, res.message)
	w.Header().Set("Content-Type", mediaProtobuf)
	w.WriteHeader(res.status)
	_, _ = w.Write(b)
}

func writeOTLPResponse(w http.ResponseWriter, res result, asJSON bool) {
	if asJSON {
		w.Header().Set("Content-Type", mediaJSON)
		out := map[string]any{}
		if res.rejected > 0 {
			out["partialSuccess"] = map[string]any{"rejectedSpans": strconv.Itoa(res.rejected), "errorMessage": res.why}
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	w.Header().Set("Content-Type", mediaProtobuf)
	_, _ = w.Write(exportResponse(res)) //nolint:gosec // G705: protobuf, served as application/x-protobuf with nosniff
}

// exportResponse encodes an ExportTraceServiceResponse: empty, or with
// partial_success saying how many spans were refused and why.
func exportResponse(res result) []byte {
	var b []byte
	if res.rejected > 0 {
		var partial []byte
		partial = protowire.AppendTag(partial, 1, protowire.VarintType)
		partial = protowire.AppendVarint(partial, uint64(res.rejected)) //nolint:gosec // G115: a count
		partial = protowire.AppendTag(partial, 2, protowire.BytesType)
		partial = protowire.AppendString(partial, res.why)
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, partial)
	}
	return b
}

// ZipkinHandler serves Zipkin's POST /api/v2/spans, in JSON or protobuf.
// It answers 202 when the spans are stored, as Zipkin does.
func (s *Server) ZipkinHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/spans", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !onlyPost(w, r) {
			return
		}
		res := s.ingest(r, "zipkin", decodeZipkin)
		if res.status != http.StatusOK {
			retryAfter(w, res.retryAfter)
			http.Error(w, res.message, res.status)
			return
		}
		if res.rejected > 0 {
			// Zipkin has no partial success; say what was left out.
			w.Header().Set("X-Signalbox-Rejected-Spans", strconv.Itoa(res.rejected))
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Send spans with POST /api/v2/spans.", http.StatusNotFound)
	})
	return mux
}

func decodeZipkin(body []byte, mediaType string, accept window) ([]telemetry.Span, int, string, error) {
	var (
		in  []zipkinSpan
		err error
	)
	switch mediaType {
	case mediaJSON, "":
		in, err = decodeZipkinJSON(body)
	case mediaProtobuf:
		in, err = decodeZipkinProtobuf(body)
	default:
		return nil, 0, "", fmt.Errorf("%w: send Zipkin spans as %s or %s", errUnsupported, mediaJSON, mediaProtobuf)
	}
	if err != nil {
		return nil, 0, "", err
	}
	spans, rejected, why := fromZipkin(in, accept)
	return spans, rejected, why, nil
}
