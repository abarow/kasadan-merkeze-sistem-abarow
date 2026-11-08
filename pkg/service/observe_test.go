// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	HealthHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics: %d", rec.Code)
	}
	return rec.Body.String()
}

// An HTTP request gets an id (or keeps a plain one it brought), the answer returns it,
// the handler sees it, and the request is counted by its route.
func TestObserveHTTP(t *testing.T) {
	var seen string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /t/sales/{id}", func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
		if r.PathValue("id") == "bad" {
			http.Error(w, "boom", http.StatusServiceUnavailable)
		}
	})
	h := observeHTTP(mux)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/t/sales/1", nil))
	id := rec.Header().Get(RequestIDHeader)
	if len(id) != 24 || seen != id {
		t.Errorf("new id %q, handler saw %q", id, seen)
	}
	req := httptest.NewRequest("GET", "/t/sales/bad", nil)
	req.Header.Set(RequestIDHeader, "screen-42")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get(RequestIDHeader) != "screen-42" || seen != "screen-42" {
		t.Errorf("kept id = %q, handler saw %q", rec.Header().Get(RequestIDHeader), seen)
	}
	req = httptest.NewRequest("GET", "/t/sales/1", nil)
	req.Header.Set(RequestIDHeader, "bad id\nwith newline")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(RequestIDHeader); len(got) != 24 {
		t.Errorf("an unsafe id was kept: %q", got)
	}

	out := scrape(t)
	for _, want := range []string{
		`abarow_http_requests_total{route="GET /t/sales/{id}",code="200"} 2`,
		`abarow_http_requests_total{route="GET /t/sales/{id}",code="503"} 1`,
		`abarow_http_request_duration_seconds_count{route="GET /t/sales/{id}"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A gRPC call keeps the id that came in metadata (or gets one), passes it on to the
// calls it makes, and is counted by method and code.
func TestObserveGRPC(t *testing.T) {
	var seen, passed string
	call := func(ctx context.Context, _ any) (any, error) {
		seen = RequestID(ctx)
		// The service calls another one with the same context.
		_ = passIDUnary(ctx, "/t.Other/Get", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, _ := metadata.FromOutgoingContext(ctx)
			passed = strings.Join(md.Get(requestIDKey), ",")
			return nil
		})
		return nil, status.Error(codes.Internal, "boom")
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/t.Sales/Pay"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestIDKey, "abc-1"))
	if _, err := observeUnary(ctx, nil, info, call); status.Code(err) != codes.Internal {
		t.Fatalf("err = %v", err)
	}
	if seen != "abc-1" || passed != "abc-1" {
		t.Errorf("handler saw %q, passed on %q; abc-1 expected", seen, passed)
	}
	if _, err := observeUnary(context.Background(), nil, info, func(ctx context.Context, _ any) (any, error) {
		seen = RequestID(ctx)
		return nil, nil
	}); err != nil || len(seen) != 24 {
		t.Errorf("new id %q, %v", seen, err)
	}
	out := scrape(t)
	for _, want := range []string{
		`abarow_grpc_requests_total{method="/t.Sales/Pay",code="Internal"} 1`,
		`abarow_grpc_requests_total{method="/t.Sales/Pay",code="OK"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// A call without a request id passes nothing on.
func TestPassIDWithoutID(t *testing.T) {
	_ = passIDUnary(context.Background(), "/t/M", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(requestIDKey)) > 0 {
			t.Errorf("an id was passed: %v", md)
		}
		return nil
	})
}
