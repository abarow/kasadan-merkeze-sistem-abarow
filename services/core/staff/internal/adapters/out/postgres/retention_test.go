// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/retention"
	"abarow.com/services/core/staff/internal/adapters/out/eventproto"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// TestPruneDays: the details of a day closed before the moment go once the store server
// confirmed the shift events; the period and the shift stay and their reports say the
// details are gone. The open day is not touched.
func TestPruneDays(t *testing.T) {
	ctx := context.Background()
	_, st, now := newAuth(t) // the shift clock starts on 25.10.2026, before the day closes
	shifts := app.NewShiftService(st, st, app.Events{})
	shifts.Now = func() time.Time { return *now }
	shifts.Drawer = &fakeDrawer{}
	n := 0
	shifts.Announcer = eventproto.Encoder{StoreID: "store-1", RegisterID: "register-2", NewID: func() string { n++; return fmt.Sprintf("ev-%d", n) }}
	svc := app.NewService(st, st, st, app.Events{})
	start := *now

	sh, err := shifts.Open(ctx, "ayse", 50000)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordCompleted(ctx, cashSale("s1", "ayse", 1, 1000, start.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordVoided(ctx, domain.VoidedSale{SaleID: "v1", CashierID: "ayse", Total: 400, At: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := shifts.RecordCashMovement(ctx, "ayse", domain.CashIn, 10000, "Bozuk para"); err != nil {
		t.Fatal(err)
	}
	if _, err := shifts.OpenDrawer(ctx, "ali", "ayse", "Bozuk para"); err != nil {
		t.Fatal(err)
	}
	*now = start.Add(8 * time.Hour)
	if _, err := shifts.Close(ctx, "ayse", 61000); err != nil {
		t.Fatal(err)
	}
	z, err := svc.CloseDay(ctx, "ali") // closed now (the service's clock)
	if err != nil {
		t.Fatal(err)
	}
	// The next day: a shift is open and has a sale.
	*now = start.Add(9 * time.Hour)
	if _, err := shifts.Open(ctx, "ayse", 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordCompleted(ctx, cashSale("s2", "ayse", 2, 700, start.Add(9*time.Hour+time.Second))); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Add(time.Hour) // the closed day is older than this
	// The shift events have no receipt yet: nothing goes.
	if got, err := st.PruneDays(ctx, before); !errors.Is(err, retention.ErrSkipped) || got != 0 {
		t.Fatalf("PruneDays without receipts = %d, %v", got, err)
	}
	if r, err := svc.DayReport(ctx, z.ZNo); err != nil || r.ReceiptCount != 1 {
		t.Fatalf("Z report before pruning: %+v, %v", r, err)
	}

	pending, err := st.Pending(ctx, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("outbox: %d, %v; 3 shift events expected", len(pending), err)
	}
	for _, p := range pending {
		if err := st.MarkPublished(ctx, p.Seq, start); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveReceived(ctx, delivery.Receipt{EventID: p.ID, ReceiptID: "r-" + p.ID, At: start}); err != nil {
			t.Fatal(err)
		}
	}
	// One sale, one void, one cash movement, one drawer opening.
	if got, err := st.PruneDays(ctx, before); err != nil || got != 4 {
		t.Fatalf("PruneDays = %d, %v; 4 rows expected", got, err)
	}
	if _, err := svc.DayReport(ctx, z.ZNo); !errors.Is(err, domain.ErrDetailsRemoved) {
		t.Errorf("Z report after pruning: %v; ErrDetailsRemoved expected (not a zero report)", err)
	}
	if _, err := shifts.Report(ctx, "ali", sh.ID); !errors.Is(err, domain.ErrDetailsRemoved) {
		t.Errorf("shift report after pruning: %v", err)
	}
	// The open day and its shift are untouched.
	if x, err := svc.XReport(ctx); err != nil || x.ReceiptCount != 1 || x.Total != 700 {
		t.Errorf("X report of the open day: %+v, %v", x, err)
	}
	if cur, err := shifts.Current(ctx); err != nil || cur.DetailsRemoved {
		t.Errorf("open shift: %+v, %v", cur, err)
	}
	if got, err := st.PruneDays(ctx, before); err != nil || got != 0 {
		t.Errorf("second PruneDays = %d, %v", got, err)
	}

	// The confirmed events and the receipts go after their own times.
	if got, err := st.PruneOutbox(ctx, before); err != nil || got != 3 {
		t.Errorf("PruneOutbox = %d, %v", got, err)
	}
	if got, err := st.PruneReceipts(ctx, before); err != nil || got != 3 {
		t.Errorf("PruneReceipts = %d, %v", got, err)
	}
}
