// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"
	"time"
)

// TestPruneEventPayloads: the raw content of the old events goes, the rows and the sales
// ledger stay, and an old event that comes again is still recognised.
func TestPruneEventPayloads(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for i, id := range []string{"e1", "e2"} {
		if _, err := s.Record(ctx, saleEvent(id, "store-1", "register-1"), sale("s"+id, "store-1", "register-1", int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	// e1 came 100 days ago, e2 now (received_at is set by the database).
	if _, err := s.db.ExecContext(ctx, `UPDATE events SET received_at = now() - interval '100 days' WHERE id = 'e1'`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-90 * 24 * time.Hour)
	if n, err := s.PruneEventPayloads(ctx, before); err != nil || n != 1 {
		t.Fatalf("PruneEventPayloads = %d, %v", n, err)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM events WHERE payload IS NULL`); n != 1 {
		t.Errorf("emptied = %d, want 1", n)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM events WHERE id = 'e2' AND payload IS NOT NULL`); n != 1 {
		t.Error("the content of the new event went")
	}
	if n := queryInt(t, s, `SELECT count(*) FROM sales`); n != 2 {
		t.Errorf("sales = %d; the ledger is never pruned", n)
	}
	// The old event comes again: recognised, not written twice.
	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("se1", "store-1", "register-1", 1)); err != nil || fresh {
		t.Errorf("repeated old event = %v, %v; not fresh expected", fresh, err)
	}
	if n, err := s.PruneEventPayloads(ctx, before); err != nil || n != 0 {
		t.Errorf("second PruneEventPayloads = %d, %v", n, err)
	}
	// The receipts: both were given now, so they stay; the published outbox has none yet.
	if n, err := s.PruneReceipts(ctx, before); err != nil || n != 0 {
		t.Errorf("PruneReceipts = %d, %v", n, err)
	}
	if n, err := s.PruneOutbox(ctx, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Errorf("PruneOutbox (nothing published) = %d, %v", n, err)
	}
}
