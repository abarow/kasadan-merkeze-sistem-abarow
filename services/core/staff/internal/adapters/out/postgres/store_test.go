// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

func sale(id string, receiptNo int64, total money.Money) domain.CompletedSale {
	return domain.CompletedSale{
		SaleID: id, ReceiptNo: receiptNo, Total: total, TaxTotal: total / 100,
		Taxes:    []domain.TaxTotal{{Rate: 100, Gross: total, Tax: total / 100}},
		Payments: []domain.PaymentTotal{{Method: domain.Card, Amount: total}},
		At:       time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC),
	}
}

func TestReportsAcrossDays(t *testing.T) {
	ctx := context.Background()
	_, st, _ := newAuth(t) // a manager takes the Z: the cashiers are loaded
	svc := app.NewService(st, st, st, app.Events{})

	// Events come at least once: the same sale is processed twice, counted once.
	for _, s := range []domain.CompletedSale{sale("s1", 1, 1000), sale("s2", 2, 2500), sale("s1", 1, 1000)} {
		if err := svc.RecordCompleted(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.RecordVoided(ctx, domain.VoidedSale{SaleID: "s3", Total: 400, At: time.Now()}); err != nil {
		t.Fatal(err)
	}

	x, err := svc.XReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if x.ZNo != 1 || x.ReceiptCount != 2 || x.Total != 3500 || x.VoidedCount != 1 || !x.ClosedAt.IsZero() {
		t.Fatalf("X raporu yanlış: %+v", x)
	}

	z, err := svc.CloseDay(ctx, "ali")
	if err != nil {
		t.Fatal(err)
	}
	if z.ZNo != 1 || z.ReceiptCount != 2 || z.Total != 3500 || z.ClosedAt.IsZero() {
		t.Fatalf("Z raporu yanlış: %+v", z)
	}

	// After Z the new period starts empty; new sales go into it.
	if err := svc.RecordCompleted(ctx, sale("s4", 3, 700)); err != nil {
		t.Fatal(err)
	}
	x, err = svc.XReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if x.ZNo != 2 || x.ReceiptCount != 1 || x.Total != 700 || x.VoidedCount != 0 {
		t.Errorf("Z sonrası X raporu yanlış: %+v", x)
	}
}
