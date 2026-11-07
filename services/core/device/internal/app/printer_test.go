// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"abarow.com/services/core/device/internal/adapters/out/simulator"
	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

// next, dinleyiciden bir sonraki durumu bekler.
func next(t *testing.T, ch <-chan domain.PrinterState) domain.PrinterState {
	t.Helper()
	select {
	case st := <-ch:
		return st
	case <-time.After(3 * time.Second):
		t.Fatal("the state did not come")
		return 0
	}
}

// The printer's state goes to the listeners; a printer that is out of paper does not print, and
// when the paper is loaded it prints again.
func TestPrinterState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var paper bytes.Buffer
	svc := app.NewService(simulator.NewPrinter(&paper, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})
	watch := svc.WatchPrinter(ctx)
	if st := next(t, watch); st != domain.PrinterUnknown {
		t.Errorf("not asked yet: %v", st)
	}
	go svc.RunPrinterMonitor(ctx, time.Hour)
	if st := next(t, watch); st != domain.PrinterReady {
		t.Errorf("ilk soru: %v", st)
	}

	if err := svc.SimulatePrinter(ctx, domain.PrinterPaperOut); err != nil {
		t.Fatal(err)
	}
	if st := next(t, watch); st != domain.PrinterPaperOut || st.Message() != "Kağıt bitti" {
		t.Errorf("paper out: %v %q", st, st.Message())
	}
	doc := domain.Document{Title: "Fiş 1", Lines: []string{"TOPLAM *49,00"}}
	if err := svc.Print(ctx, doc, ""); err == nil || paper.Len() != 0 {
		t.Errorf("printed without paper: %v", err)
	}
	if st := svc.Status(); st.PrinterState != domain.PrinterPaperOut || st.Printed != 0 {
		t.Errorf("durum: %+v", st)
	}

	if err := svc.SimulatePrinter(ctx, domain.PrinterReady); err != nil {
		t.Fatal(err)
	}
	if st := next(t, watch); st != domain.PrinterReady {
		t.Errorf("paper loaded: %v", st)
	}
	if err := svc.Print(ctx, doc, ""); err != nil {
		t.Fatal(err)
	}

	// A new listener gets the current state immediately.
	if st := next(t, svc.WatchPrinter(ctx)); st != domain.PrinterReady {
		t.Errorf("yeni dinleyici: %v", st)
	}
	if err := svc.SimulatePrinter(ctx, domain.PrinterState(42)); !errors.Is(err, domain.ErrInvalidPrinterState) {
		t.Errorf("invalid state: %v", err)
	}
}

// statePrinter is a printer whose state can be asked but which is not a simulator.
type statePrinter struct {
	app.Printer
	state chan domain.PrinterState
}

func (p statePrinter) State(context.Context) domain.PrinterState { return <-p.state }

// After a print failure the state is asked again without waiting.
func TestPrinterRecheckAfterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sim := simulator.NewPrinter(&bytes.Buffer{}, 32)
	p := statePrinter{Printer: sim, state: make(chan domain.PrinterState, 1)}
	svc := app.NewService(p, simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})
	watch := svc.WatchPrinter(ctx)
	next(t, watch)

	p.state <- domain.PrinterReady
	go svc.RunPrinterMonitor(ctx, time.Hour)
	if st := next(t, watch); st != domain.PrinterReady {
		t.Fatalf("ilk soru: %v", st)
	}
	// The cover opened: printing fails, the state comes without waiting for the hourly query.
	sim.SetState(domain.PrinterCoverOpen)
	p.state <- domain.PrinterCoverOpen
	if err := svc.Print(ctx, domain.Document{Lines: []string{"x"}}, ""); err == nil {
		t.Fatal("printed with the cover open")
	}
	if st := next(t, watch); st != domain.PrinterCoverOpen {
		t.Errorf("after a print failure: %v", st)
	}
	if err := svc.SimulatePrinter(ctx, domain.PrinterReady); !errors.Is(err, domain.ErrNoPrinterSimulator) {
		t.Errorf("printer that is not a simulator: %v", err)
	}
}
