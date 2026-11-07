// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"abarow.com/services/core/device/internal/adapters/out/simulator"
	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

func TestPrintAndDrawer(t *testing.T) {
	ctx := context.Background()
	var paper bytes.Buffer
	svc := app.NewService(simulator.NewPrinter(&paper, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})

	if err := svc.Print(ctx, domain.Document{Title: "Fiş 1", Lines: []string{"ÖRNEK MARKET", "TOPLAM *49,00"}}, ""); err != nil {
		t.Fatal(err)
	}
	// A document that is not validated never goes to the printer.
	before := paper.Len()
	if err := svc.Print(ctx, domain.Document{Lines: []string{strings.Repeat("x", 33)}}, ""); !errors.Is(err, domain.ErrInvalidDocument) {
		t.Errorf("wide line: %v", err)
	}
	if paper.Len() != before {
		t.Error("an invalid document was written to paper")
	}
	if err := svc.OpenDrawer(ctx, "nakit satış"); err != nil {
		t.Fatal(err)
	}

	if st := svc.Status(); st.Printer != "simulator" || st.Columns != 32 || st.Printed != 1 || st.DrawerOpened != 1 {
		t.Errorf("state is wrong: %+v", st)
	}
	out := paper.String()
	if !strings.Contains(out, "Fiş 1") || !strings.Contains(out, "|ÖRNEK MARKET"+strings.Repeat(" ", 20)+"|") {
		t.Errorf("paper output:\n%s", out)
	}
}

// The code read goes to all the listeners; an invalid read is discarded; when the listener
// leaves its channel closes. A scan is imitated only on the simulator.
func TestScans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sim := simulator.NewScanner()
	svc := app.NewService(simulator.NewPrinter(&bytes.Buffer{}, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, sim)
	go svc.RunScanner(ctx)

	ctxA, stopA := context.WithCancel(ctx)
	a, b := svc.Watch(ctxA), svc.Watch(ctx)
	if err := svc.Simulate("8690000000012"); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan string{"a": a, "b": b} {
		select {
		case code := <-ch:
			if code != "8690000000012" {
				t.Errorf("%s: %q", name, code)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not get the read", name)
		}
	}
	for _, bad := range []string{"", "ç123", strings.Repeat("1", 129)} {
		if err := svc.Simulate(bad); !errors.Is(err, domain.ErrInvalidScan) {
			t.Errorf("invalid code %q: %v", bad, err)
		}
	}
	stopA()
	if _, open := <-a; open {
		t.Error("the channel of the listener that left did not close")
	}
	if st := svc.Status(); st.Scanner != "simulator" || st.Scanned != 1 {
		t.Errorf("durum %+v", st)
	}

	// No imitation on a real reader.
	real := app.NewService(simulator.NewPrinter(&bytes.Buffer{}, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})
	if err := real.Simulate("123"); !errors.Is(err, domain.ErrNoSimulator) {
		t.Errorf("imitation on a real reader: %v", err)
	}
}

// If the station printer is installed the station's document is printed on it, if not on the
// receipt printer.
func TestStationPrinters(t *testing.T) {
	ctx := context.Background()
	var receipt, kitchen bytes.Buffer
	svc := app.NewService(simulator.NewPrinter(&receipt, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})
	svc.WithStation("kitchen", simulator.NewPrinter(&kitchen, 32))
	if err := svc.Print(ctx, domain.Document{Title: "Mutfak: 5", Lines: []string{"2 x Köfte"}}, "kitchen"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Print(ctx, domain.Document{Title: "Bar: 5", Lines: []string{"1 x Ayran"}}, "bar"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kitchen.String(), "Köfte") || strings.Contains(kitchen.String(), "Ayran") || !strings.Contains(receipt.String(), "Ayran") {
		t.Errorf("kitchen:\n%s\nreceipt:\n%s", kitchen.String(), receipt.String())
	}
}
