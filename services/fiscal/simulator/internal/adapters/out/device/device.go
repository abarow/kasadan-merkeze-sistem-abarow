// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package device holds the app.Printer and app.Drawer adapters that print the receipt on the
// printer of the device service and have the cash drawer opened.
package device

import (
	"context"
	"log/slog"
	"strings"
	"time"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/fiscal/simulator/internal/app"
)

// Printer prints the receipt on device.
type Printer struct {
	Client devicev1.DeviceServiceClient
}

var _ app.Printer = Printer{}

func (p Printer) Print(ctx context.Context, title, text string) error {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	_, err := p.Client.Print(ctx, &devicev1.PrintRequest{Title: title, Lines: lines})
	return service.RemoteErrorFrom("yazıcı", err)
}

// Drawer has the drawer opened on device. If it cannot be opened the receipt is still valid;
// the error is written to the log (the cashier opens the drawer by hand).
type Drawer struct {
	Client devicev1.DeviceServiceClient
	Log    *slog.Logger
}

var _ app.Drawer = Drawer{}

func (d Drawer) Open(ctx context.Context, reason string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := d.Client.OpenDrawer(ctx, &devicev1.OpenDrawerRequest{Reason: reason}); err != nil {
		d.Log.Warn("could not open the drawer", "reason", reason, "err", service.RemoteErrorFrom("çekmece", err))
	}
}

// Columns returns the number of columns of the printer on device; the receipt is laid out
// at this width.
func Columns(ctx context.Context, c devicev1.DeviceServiceClient) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := c.GetStatus(ctx, &devicev1.GetStatusRequest{})
	return int(st.GetColumns()), service.RemoteErrorFrom("yazıcı", err)
}
