// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"abarow.com/services/fiscal/simulator/internal/adapters/out/memory"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/writer"
	"abarow.com/services/fiscal/simulator/internal/app"
	"abarow.com/services/fiscal/simulator/internal/domain"
)

func receipt() domain.Receipt {
	return domain.Receipt{
		SaleID:     "s1",
		RegisterID: "register-1",
		Lines:      []domain.Line{{Name: "Ekmek", Qty: 1000, UnitPrice: 1500, Total: 1500, TaxRate: 100}},
		Taxes:      []domain.TaxLine{{Rate: 100, Gross: 1500, Tax: 15}},
		Total:      1500,
		Payments:   []domain.Payment{{Method: "card", Amount: 1500}},
		At:         time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC),
	}
}

func TestRegisterPrintsNonFiscalReceipt(t *testing.T) {
	var out strings.Builder
	drawer := &countingDrawer{}
	svc := app.NewService(&memory.Counter{}, writer.New(&out), drawer, domain.Layout{Width: 48})
	ctx := context.Background()

	for _, want := range []string{"SIM-000001", "SIM-000002"} {
		no, err := svc.Register(ctx, receipt())
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if no != want {
			t.Errorf("numara %q, istenen %q", no, want)
		}
	}
	if got := strings.Count(out.String(), domain.Warning); got != 4 {
		t.Errorf("%d warnings on two receipts, want 4 (the start and end of each receipt)", got)
	}
	// On a receipt with a card the drawer is not opened; on one with cash it is.
	if len(drawer.reasons) != 0 {
		t.Errorf("the drawer opened on a receipt with a card: %v", drawer.reasons)
	}
	cash := receipt()
	cash.Payments = []domain.Payment{{Method: "cash", Amount: 2000}}
	cash.Change = 500
	no, err := svc.Register(ctx, cash)
	if err != nil {
		t.Fatal(err)
	}
	if len(drawer.reasons) != 1 || !strings.Contains(drawer.reasons[0], no) {
		t.Errorf("drawer on a receipt with cash: %v", drawer.reasons)
	}
}

type countingDrawer struct{ reasons []string }

func (d *countingDrawer) Open(_ context.Context, reason string) {
	d.reasons = append(d.reasons, reason)
}

type failingPrinter struct{}

func (failingPrinter) Print(context.Context, string, string) error {
	return errors.New("kağıt bitti")
}

func TestRegisterErrors(t *testing.T) {
	ctx := context.Background()

	var out strings.Builder
	svc := app.NewService(&memory.Counter{}, writer.New(&out), &countingDrawer{}, domain.Layout{Width: 48})
	bad := receipt()
	bad.Total = 1
	if _, err := svc.Register(ctx, bad); !errors.Is(err, domain.ErrInvalidReceipt) {
		t.Errorf("inconsistent receipt: err = %v, want %v", err, domain.ErrInvalidReceipt)
	}
	if out.Len() != 0 {
		t.Error("an inconsistent receipt was printed")
	}

	drawer := &countingDrawer{}
	svc = app.NewService(&memory.Counter{}, failingPrinter{}, drawer, domain.Layout{Width: 48})
	cash := receipt()
	cash.Payments = []domain.Payment{{Method: "cash", Amount: 1500}}
	if _, err := svc.Register(ctx, cash); err == nil {
		t.Error("no printer error returned")
	}
	if len(drawer.reasons) != 0 {
		t.Error("the drawer opened although the receipt could not be printed")
	}
}
