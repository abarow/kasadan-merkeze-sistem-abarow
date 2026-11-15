// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package device_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/device"
)

type deviceServer struct {
	devicev1.UnimplementedDeviceServiceServer
	printed *devicev1.PrintRequest
	reason  string
	failing bool
}

func (d *deviceServer) Print(_ context.Context, r *devicev1.PrintRequest) (*devicev1.PrintResponse, error) {
	if d.failing {
		return nil, status.Error(codes.FailedPrecondition, "kağıt bitti")
	}
	d.printed = r
	return &devicev1.PrintResponse{}, nil
}

func (d *deviceServer) OpenDrawer(_ context.Context, r *devicev1.OpenDrawerRequest) (*devicev1.OpenDrawerResponse, error) {
	if d.failing {
		return nil, status.Error(codes.Unavailable, "no drawer")
	}
	d.reason = r.GetReason()
	return &devicev1.OpenDrawerResponse{}, nil
}

func (d *deviceServer) GetStatus(context.Context, *devicev1.GetStatusRequest) (*devicev1.GetStatusResponse, error) {
	if d.failing {
		return nil, status.Error(codes.Unavailable, "cihaz yok")
	}
	return &devicev1.GetStatusResponse{Columns: 42}, nil
}

func TestDeviceAdapters(t *testing.T) {
	srv := &deviceServer{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { devicev1.RegisterDeviceServiceServer(s, srv) })
	client := devicev1.NewDeviceServiceClient(conn)
	ctx := context.Background()

	if err := (device.Printer{Client: client}).Print(ctx, "Fiş SIM-1", "satır 1\nsatır 2\n"); err != nil {
		t.Fatal(err)
	}
	if srv.printed.GetTitle() != "Fiş SIM-1" || len(srv.printed.GetLines()) != 2 || srv.printed.GetLines()[1] != "satır 2" {
		t.Errorf("printed: %v", srv.printed)
	}
	var logs bytes.Buffer
	d := device.Drawer{Client: client, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	d.Open(ctx, "nakit satış")
	if srv.reason != "nakit satış" || logs.Len() != 0 {
		t.Errorf("drawer: %q (log %q)", srv.reason, logs.String())
	}
	if cols, err := device.Columns(ctx, client); err != nil || cols != 42 {
		t.Errorf("Columns: %d %v", cols, err)
	}

	// When the device gives an error: a printer error is returned; a drawer error is only written to the log.
	srv.failing = true
	if err := (device.Printer{Client: client}).Print(ctx, "x", "y"); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("printer error: %v", err)
	}
	d.Open(ctx, "x")
	if !strings.Contains(logs.String(), "could not open the drawer") {
		t.Errorf("the drawer error was not logged: %q", logs.String())
	}
	if _, err := device.Columns(ctx, client); err == nil {
		t.Error("the Columns error was not returned")
	}
}
