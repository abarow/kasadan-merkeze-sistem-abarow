// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package device

import (
	"context"
	"testing"
	"time"

	devicev1 "abarow.com/gen/com/abarow/device/v1"
	"google.golang.org/grpc"
)

// hang is a device that does not return until the context ends (a frozen service).
type hang struct{ devicev1.DeviceServiceClient }

func (hang) Print(ctx context.Context, _ *devicev1.PrintRequest, _ ...grpc.CallOption) (*devicev1.PrintResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (hang) OpenDrawer(ctx context.Context, _ *devicev1.OpenDrawerRequest, _ ...grpc.CallOption) (*devicev1.OpenDrawerResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDeviceCallsAreBoundedWithoutCallerDeadline(t *testing.T) {
	printTimeout, drawerTimeout = 100*time.Millisecond, 100*time.Millisecond
	done := make(chan error, 2)
	go func() { done <- Printer{Client: hang{}}.Print(context.Background(), "x", nil) }()
	go func() { done <- Drawer{Client: hang{}}.Open(context.Background(), "x") }()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Error("the frozen service gave no error")
			}
		case <-time.After(printTimeout + 3*time.Second):
			t.Fatal("the call did not end within its time limit")
		}
	}
}
