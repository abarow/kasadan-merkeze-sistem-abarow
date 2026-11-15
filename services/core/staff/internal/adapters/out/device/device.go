// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package device holds the adapters that print reports on the device service's receipt
// printer (app.Printer) and open the cash drawer (app.Drawer).
package device

import (
	"context"
	"errors"
	"time"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/core/staff/internal/app"
)

// Even if the caller has no time limit a frozen device service does not hang staff (and
// the closing report) forever.
var (
	printTimeout  = 15 * time.Second
	drawerTimeout = 5 * time.Second
)

// Printer is device's printer.
type Printer struct {
	Client devicev1.DeviceServiceClient
}

var _ app.Printer = Printer{}

func (p Printer) Columns(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, printTimeout)
	defer cancel()
	st, err := p.Client.GetStatus(ctx, &devicev1.GetStatusRequest{})
	return int(st.GetColumns()), service.RemoteErrorFrom("yazıcı", err)
}

func (p Printer) Print(ctx context.Context, title string, lines []string) error {
	ctx, cancel := context.WithTimeout(ctx, printTimeout)
	defer cancel()
	_, err := p.Client.Print(ctx, &devicev1.PrintRequest{Title: title, Lines: lines})
	return service.RemoteErrorFrom("yazıcı", err)
}

// ErrNoPrinter is returned when printing is requested without a device address being given.
var ErrNoPrinter = errors.New("yazıcı yapılandırılmamış (ABAROW_DEVICE_ADDR)")

// None is used when there is no printer; every print returns ErrNoPrinter.
type None struct{}

var _ app.Printer = None{}

func (None) Columns(context.Context) (int, error)          { return 0, ErrNoPrinter }
func (None) Print(context.Context, string, []string) error { return ErrNoPrinter }

// Drawer is device's cash drawer.
type Drawer struct {
	Client devicev1.DeviceServiceClient
}

var _ app.Drawer = Drawer{}

func (d Drawer) Open(ctx context.Context, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, drawerTimeout)
	defer cancel()
	_, err := d.Client.OpenDrawer(ctx, &devicev1.OpenDrawerRequest{Reason: reason})
	return service.RemoteErrorFrom("çekmece", err)
}
