// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A panicking handler gives an Internal error; the service keeps running.
func TestRecoverPanics(t *testing.T) {
	_, err := recoverUnary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/t/Unary"},
		func(context.Context, any) (any, error) { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Errorf("unary: %v", err)
	}
	err = recoverStream(nil, nil, &grpc.StreamServerInfo{FullMethod: "/t/Stream"},
		func(any, grpc.ServerStream) error { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Errorf("stream: %v", err)
	}
	if _, err := recoverUnary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/t/Unary"},
		func(context.Context, any) (any, error) { return "ok", nil }); err != nil {
		t.Errorf("no panic: %v", err)
	}
}

// The health port tells the release the image was built with.
func TestVersionEndpoint(t *testing.T) {
	old := Version
	Version = "1.2.3"
	defer func() { Version = old }()
	rec := httptest.NewRecorder()
	HealthHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/version", nil))
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got["version"] != "1.2.3" || got["go"] == "" {
		t.Errorf("/version: %s, %v", rec.Body.String(), err)
	}
}
