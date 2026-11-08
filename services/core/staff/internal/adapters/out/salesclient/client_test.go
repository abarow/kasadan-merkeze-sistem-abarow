// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package salesclient

import (
	"context"
	"testing"
	"time"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	"google.golang.org/grpc"
)

// hang is a sales that does not return until the context ends (a frozen service).
type hang struct{ salesv1.SalesServiceClient }

func (hang) GetOutboxStatus(ctx context.Context, _ *salesv1.GetOutboxStatusRequest, _ ...grpc.CallOption) (*salesv1.GetOutboxStatusResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (hang) ListSales(ctx context.Context, _ *salesv1.ListSalesRequest, _ ...grpc.CallOption) (*salesv1.ListSalesResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Even if the caller sets no time limit a frozen sales does not hang the closing forever.
func TestQueriesAreBoundedWithoutCallerDeadline(t *testing.T) {
	queryTimeout = 100 * time.Millisecond
	done := make(chan error, 2)
	go func() { _, err := Outbox{Client: hang{}}.PendingEvents(context.Background()); done <- err }()
	go func() { _, err := Charged{Client: hang{}}.CountCharged(context.Background()); done <- err }()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Error("the frozen service gave no error")
			}
		case <-time.After(queryTimeout + 3*time.Second):
			t.Fatal("the call did not end within its time limit")
		}
	}
}
