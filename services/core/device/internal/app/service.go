// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the device service's use cases and ports.
package app

import (
	"context"
	"sync"
	"sync/atomic"

	"abarow.com/services/core/device/internal/domain"
)

// Printer is the receipt printer (simulator or ESC/POS).
type Printer interface {
	// Name is the printer type ("simulator", "escpos" ...).
	Name() string
	// Columns is the largest number of characters in a line.
	Columns() int
	// Print prints the validated document.
	Print(ctx context.Context, doc domain.Document) error
}

// Drawer is the cash drawer. In a real install it is mostly connected to the receipt
// printer and is opened with a command sent to the printer.
type Drawer interface {
	Open(ctx context.Context, reason string) error
}

// Status is the state of the devices.
type Status struct {
	Printer      string
	Columns      int
	Printed      int64
	DrawerOpened int64
	Scanner      string
	// PrinterState is the printer's last known state.
	PrinterState domain.PrinterState
	// Scale is the scale type ("none": no scale); Weighed is the number of weighings.
	Scale   string
	Weighed int64
	// Scanned is the number of reads that went to listeners; Rejected is the number of reads
	// discarded as invalid (since the service started).
	Scanned, Rejected int64
}

// Service holds the peripheral use cases.
type Service struct {
	printer Printer
	// stations are the station printers (restaurant: kitchen, bar); if not installed
	// the station's document is printed on the receipt printer.
	stations map[string]Printer
	drawer   Drawer
	scanner  Scanner
	scale    Scale // terazisiz kasada nil

	printed, drawerOpened, scanned, rejected, weighed atomic.Int64

	// recheck is the request to ask the printer's state without waiting.
	recheck chan struct{}

	mu              sync.Mutex
	watchers        map[chan string]struct{}
	printerState    domain.PrinterState
	printerWatchers map[chan domain.PrinterState]struct{}
}

// NewService connects the ports.
func NewService(printer Printer, drawer Drawer, scanner Scanner) *Service {
	return &Service{
		printer: printer, drawer: drawer, scanner: scanner,
		recheck:         make(chan struct{}, 1),
		watchers:        map[chan string]struct{}{},
		printerWatchers: map[chan domain.PrinterState]struct{}{},
	}
}

// WithStation connects the printer of a station (kitchen, bar in a restaurant).
func (s *Service) WithStation(station string, p Printer) *Service {
	if s.stations == nil {
		s.stations = map[string]Printer{}
	}
	s.stations[station] = p
	return s
}

// Print validates the document and prints it. If station is not empty and that station's
// printer is installed it prints there, otherwise on the receipt printer.
func (s *Service) Print(ctx context.Context, doc domain.Document, station string) error {
	if p, ok := s.stations[station]; ok {
		if err := doc.Validate(p.Columns()); err != nil {
			return err
		}
		if err := p.Print(ctx, doc); err != nil {
			return err
		}
		s.printed.Add(1)
		return nil
	}
	if err := doc.Validate(s.printer.Columns()); err != nil {
		return err
	}
	if err := s.printer.Print(ctx, doc); err != nil {
		// The reason (paper, cover) should show on the screen: the state is asked right away.
		s.askPrinter()
		return err
	}
	s.printed.Add(1)
	if s.PrinterState().Blocks() {
		s.askPrinter()
	}
	return nil
}

// OpenDrawer opens the cash drawer.
func (s *Service) OpenDrawer(ctx context.Context, reason string) error {
	if err := s.drawer.Open(ctx, reason); err != nil {
		return err
	}
	s.drawerOpened.Add(1)
	return nil
}

// Status returns the printer's and reader's type, the column count and the counters.
func (s *Service) Status() Status {
	return Status{
		Printer:      s.printer.Name(),
		Columns:      s.printer.Columns(),
		Printed:      s.printed.Load(),
		DrawerOpened: s.drawerOpened.Load(),
		Scanner:      s.scanner.Name(),
		PrinterState: s.PrinterState(),
		Scale:        s.scaleName(),
		Weighed:      s.weighed.Load(),
		Scanned:      s.scanned.Load(),
		Rejected:     s.rejected.Load(),
	}
}
