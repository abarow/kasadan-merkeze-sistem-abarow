// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"

	"abarow.com/services/headquarters/ingest/internal/app"
)

// RecordBatch writes the events, their receipts and their sequence numbers in one
// transaction; an event that came before is skipped as in Record; if one cannot be
// written nothing of the batch is written.
func TestRecordBatch(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil {
		t.Fatal(err)
	}
	items := []app.Item{
		{Event: saleEvent("e1", "store-1", "register-1"), Body: sale("s1", "store-1", "register-1", 1), Producer: "sales", Seq: 1},
		{Event: saleEvent("e2", "store-1", "register-1"), Body: sale("s2", "store-1", "register-1", 2), Producer: "sales", Seq: 2},
		{Event: saleEvent("e3", "store-1", "register-1"), Body: sale("s3", "store-1", "register-1", 3), Producer: "sales", Seq: 3},
	}
	if err := s.RecordBatch(ctx, items); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM sales`); n != 3 {
		t.Errorf("sales = %d, want 3 (the repeated one once)", n)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM delivery_issued`); n != 3 {
		t.Errorf("receipts = %d, want 3", n)
	}
	if n := queryInt(t, s, `SELECT to_seq FROM event_ranges WHERE store_id = 'store-1' AND producer = 'sales'`); n != 3 {
		t.Errorf("sequence range ends at %d, want 3", n)
	}

	// The second event cannot be written: the same VAT rate twice breaks the primary key
	// of sale_taxes.
	bad := sale("s5", "store-1", "register-1", 5)
	bad.Taxes = append(bad.Taxes, bad.Taxes[0])
	err := s.RecordBatch(ctx, []app.Item{
		{Event: saleEvent("e4", "store-1", "register-1"), Body: sale("s4", "store-1", "register-1", 4), Producer: "sales", Seq: 4},
		{Event: saleEvent("e5", "store-1", "register-1"), Body: bad, Producer: "sales", Seq: 5},
	})
	if err == nil {
		t.Fatal("a batch with a broken event was written")
	}
	if n := queryInt(t, s, `SELECT count(*) FROM events WHERE id = 'e4'`); n != 0 {
		t.Error("the good event of a failed batch was written")
	}
	if n := queryInt(t, s, `SELECT to_seq FROM event_ranges WHERE store_id = 'store-1' AND producer = 'sales'`); n != 3 {
		t.Errorf("the sequence of a failed batch was recorded: %d", n)
	}
}
