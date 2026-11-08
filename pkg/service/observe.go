// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"abarow.com/pkg/metrics"
)

// RequestIDHeader is the HTTP header of the request id; requestIDKey carries it in
// gRPC metadata. An id that comes in (from the screen, a proxy or the calling service)
// is kept, otherwise a new one is made; the HTTP answer returns it. A failed request is
// logged with it on every service it passed through.
const (
	RequestIDHeader = "X-Request-ID"
	requestIDKey    = "x-request-id"
)

type requestIDCtx struct{}

// RequestID returns the request id in ctx ("" if none).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDCtx{}).(string)
	return id
}

// WithRequestID returns ctx carrying the request id; the gRPC calls made with it pass
// it on (Dial).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtx{}, id)
}

// requestID keeps an incoming id if it is short and plain, otherwise makes a new one.
func requestID(in string) string {
	if len(in) > 0 && len(in) <= 64 {
		ok := true
		for _, c := range in {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				ok = false
				break
			}
		}
		if ok {
			return in
		}
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var (
	httpRequests = metrics.Default.Counter("abarow_http_requests_total", "HTTP requests by route and status.", "route", "code")
	httpDuration = metrics.Default.Histogram("abarow_http_request_duration_seconds", "HTTP request durations by route.", "route")
	grpcRequests = metrics.Default.Counter("abarow_grpc_requests_total", "gRPC calls served by method and code.", "method", "code")
	grpcDuration = metrics.Default.Histogram("abarow_grpc_request_duration_seconds", "gRPC call durations by method.", "method")
)

// observeHTTP gives every request an id, counts it and measures its duration by its
// route (the ServeMux pattern, so the number of series stays small); a request
// answered with 5xx is logged.
func observeHTTP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID(r.Header.Get(RequestIDHeader))
		w.Header().Set(RequestIDHeader, id)
		r = r.WithContext(WithRequestID(r.Context(), id))
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		h.ServeHTTP(rec, r)
		took := time.Since(start)
		route := r.Pattern
		if route == "" {
			route = "other"
		}
		httpRequests.Inc(route, strconv.Itoa(rec.code))
		httpDuration.Observe(took, route)
		if rec.code >= 500 {
			slog.Warn("HTTP request failed", "request_id", id, "method", r.Method, "route", route, "status", rec.code, "took", took.Round(time.Millisecond))
		}
	})
}

// statusRecorder keeps the status code of the answer.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the writer (Flush for event streams).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush passes on to the writer (server-sent events).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// failedCode says the code is a fault of the service, not of the request.
func failedCode(c codes.Code) bool {
	return c == codes.Internal || c == codes.Unknown || c == codes.DataLoss
}

func incomingID(ctx context.Context) context.Context {
	in := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(requestIDKey); len(v) > 0 {
			in = v[0]
		}
	}
	return WithRequestID(ctx, requestID(in))
}

func observed(ctx context.Context, method string, start time.Time, err error) {
	code := status.Code(err)
	grpcRequests.Inc(method, code.String())
	grpcDuration.Observe(time.Since(start), method)
	if failedCode(code) {
		slog.Warn("gRPC call failed", "request_id", RequestID(ctx), "method", method, "code", code.String(), "err", err)
	}
}

// observeUnary is observeHTTP for gRPC calls.
func observeUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	ctx = incomingID(ctx)
	start := time.Now()
	resp, err := h(ctx, req)
	observed(ctx, info.FullMethod, start, err)
	return resp, err
}

// observeStream is observeUnary for streaming calls.
func observeStream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	ctx := incomingID(ss.Context())
	start := time.Now()
	err := h(srv, idStream{ss, ctx})
	observed(ctx, info.FullMethod, start, err)
	return err
}

type idStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s idStream) Context() context.Context { return s.ctx }

// passIDUnary sends the request id of ctx with the call.
func passIDUnary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(outgoingID(ctx), method, req, reply, cc, opts...)
}

// passIDStream is passIDUnary for streaming calls.
func passIDStream(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(outgoingID(ctx), desc, cc, method, opts...)
}

func outgoingID(ctx context.Context) context.Context {
	if id := RequestID(ctx); id != "" {
		return metadata.AppendToOutgoingContext(ctx, requestIDKey, id)
	}
	return ctx
}
