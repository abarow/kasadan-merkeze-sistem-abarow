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
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/app/apptest"
	"abarow.com/services/core/sales/internal/domain"
)

// TestPruneSales: the finished sales older than the moment go with their lines and
// payments once every older event has the store server's receipt; open and charged
// sales and newer sales stay.
func TestPruneSales(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	cash := func(s *domain.Sale) []domain.Payment {
		return []domain.Payment{{Method: domain.PaymentCash, Amount: s.Total()}}
	}
	event := 0
	write := func(s *domain.Sale) {
		t.Helper()
		var msgs []app.OutboxMessage
		for _, e := range s.PullEvents() {
			event++
			msgs = append(msgs, outbox.Message{ID: fmt.Sprintf("ev-%d", event), Type: "com.abarow." + e.Name(), Subject: e.Name(),
				StoreID: "store-1", RegisterID: "register-1", Payload: []byte{1}, ContentType: "application/protobuf", OccurredAt: e.OccurredAt()})
		}
		if err := st.InTx(ctx, func(tx app.Tx) error {
			if err := tx.Save(ctx, s); err != nil {
				return err
			}
			return tx.AppendOutbox(ctx, msgs)
		}); err != nil {
			t.Fatal(err)
		}
	}
	old, recent := apptest.Now, time.Now()

	paid := testSale(t, "paid-old")
	if err := paid.RecordCharge(cash(paid)); err != nil {
		t.Fatal(err)
	}
	if err := paid.MarkPaid(1, "SIM-1", old); err != nil {
		t.Fatal(err)
	}
	write(paid)
	voided := testSale(t, "voided-old")
	if err := voided.Void(old); err != nil {
		t.Fatal(err)
	}
	write(voided)
	write(testSale(t, "open-old"))
	charged := testSale(t, "charged-old")
	if err := charged.RecordCharge(cash(charged)); err != nil {
		t.Fatal(err)
	}
	write(charged)
	fresh := testSale(t, "paid-recent")
	if err := fresh.RecordCharge(cash(fresh)); err != nil {
		t.Fatal(err)
	}
	if err := fresh.MarkPaid(2, "SIM-2", recent); err != nil {
		t.Fatal(err)
	}
	write(fresh)

	before := recent.Add(-time.Hour)
	// The old events have no receipt yet: nothing goes.
	if n, err := st.PruneSales(ctx, before); !errors.Is(err, retention.ErrSkipped) || n != 0 {
		t.Fatalf("PruneSales without receipts = %d, %v", n, err)
	}
	pending, err := st.Pending(ctx, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("outbox: %d, %v; 3 events expected (paid, voided, paid)", len(pending), err)
	}
	for _, p := range pending {
		if err := st.MarkPublished(ctx, p.Seq, p.OccurredAt); err != nil {
			t.Fatal(err)
		}
		if p.OccurredAt.Before(before) {
			if err := st.SaveReceived(ctx, delivery.Receipt{EventID: p.ID, ReceiptID: "r-" + p.ID, At: p.OccurredAt}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n, err := st.PruneSales(ctx, before); err != nil || n != 2 {
		t.Fatalf("PruneSales = %d, %v; the old paid and voided sales expected", n, err)
	}
	for id, want := range map[domain.SaleID]bool{"paid-old": false, "voided-old": false, "open-old": true, "charged-old": true, "paid-recent": true} {
		_, err := st.Get(ctx, id)
		if got := err == nil; got != want {
			t.Errorf("%s kept = %v, want %v (%v)", id, got, want, err)
		}
	}
	var lines int
	if err := st.db.QueryRow(`SELECT count(*) FROM sale_lines WHERE sale_id IN ('paid-old', 'voided-old')`).Scan(&lines); err != nil || lines != 0 {
		t.Errorf("lines of removed sales = %d, %v", lines, err)
	}
	// The confirmed old events leave the outbox; the newer one without a receipt stays.
	if n, err := st.PruneOutbox(ctx, recent.Add(time.Hour)); err != nil || n != 2 {
		t.Errorf("PruneOutbox = %d, %v", n, err)
	}
	if n := countOutbox(t, st); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
	if n, err := st.PruneReceipts(ctx, recent.Add(time.Hour)); err != nil || n != 2 {
		t.Errorf("PruneReceipts = %d, %v", n, err)
	}
}
