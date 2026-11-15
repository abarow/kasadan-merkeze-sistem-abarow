// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
)

// The receipt of an event coming from a register is given and written to the outbox to be published
// to the register; if the same event comes again no new one is written.
func TestIssueReceipt(t *testing.T) {
	s := postgrestest.Open(t)
	ctx := context.Background()
	s.StoreID = "store-1"
	n := 0
	s.NewID = func() string { n++; return fmt.Sprintf("receipt-%d", n) }

	if err := s.IssueReceipt(ctx, "register-1", "ABAROW_SALES", "e1"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Receipts().Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("outbox = %v, %v; one receipt expected", pending, err)
	}
	m := pending[0]
	if m.ID != "receipt-1" || m.Type != delivery.ReceiptType || m.Subject != "receipt.register-1.abarow_sales" || m.StoreID != "store-1" {
		t.Errorf("receipt message = %+v", m.Message)
	}
	r, err := delivery.ParseReceipt(m.Payload)
	if err != nil || r.EventID != "e1" || r.ReceiptID != "receipt-1" {
		t.Errorf("content = %+v, %v", r, err)
	}

	// The same event comes again: no new receipt.
	if err := s.IssueReceipt(ctx, "register-1", "ABAROW_SALES", "e1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Receipts().CountPending(ctx); c != 1 {
		t.Errorf("%d receipts pending; none must be written for a repeat", c)
	}
	// Another register's event gets its own receipt.
	if err := s.IssueReceipt(ctx, "register-2", "ABAROW_STAFF", "e2"); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.Receipts().Pending(ctx, 10)
	if len(pending) != 2 || pending[1].Subject != "receipt.register-2.abarow_staff" {
		t.Errorf("outbox = %+v", pending)
	}
	if err := s.Receipts().MarkPublished(ctx, pending[0].Seq, time.Now()); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Receipts().CountPending(ctx); c != 1 {
		t.Errorf("%d receipts pending; 1 expected", c)
	}
}

// Headquarters' receipt is written next to the event; the cleaner queries it.
func TestSaveReceived(t *testing.T) {
	s := postgrestest.Open(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if err := s.SaveReceived(ctx, delivery.Receipt{EventID: "e1", ReceiptID: "r1", At: at}); err != nil {
		t.Fatal(err)
	}
	// If the receipt comes again it does not fail, the first stays valid.
	if err := s.SaveReceived(ctx, delivery.Receipt{EventID: "e1", ReceiptID: "r1b", At: at}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Received(ctx, []string{"e1", "e2"})
	if err != nil || !got["e1"] || got["e2"] || len(got) != 1 {
		t.Fatalf("Received = %v, %v", got, err)
	}
}
