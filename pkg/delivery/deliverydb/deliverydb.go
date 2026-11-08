// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package deliverydb is the database side of the receipts (the same SQL for SQLite
// and PostgreSQL). The receiver writes the receipts it gave to delivery_issued, the sender writes
// the receipts it received to delivery_received; each service sets up the tables in its own
// migration (the schema is in testdata and is the same as the migrations).
package deliverydb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/sqldialect"
)

// Store accesses the receipt tables.
type Store struct {
	Dialect sqldialect.Dialect
}

// Issued is the receipt the receiver gave for that event.
type Issued struct {
	delivery.Receipt
	// New says the receipt was produced in this call; it is false if the event was processed
	// before too (a message that came again), and the receipt is not published again.
	New bool
}

// Issue writes to the receiver's database that the event was processed and returns the receipt;
// it is called in the same transaction as the event's record (db is a *sql.Tx). If a receipt
// already exists for the event it returns it (New false). owner and stream say whom the
// receipt is for and for which stream.
func (s Store) Issue(ctx context.Context, db sqldialect.DB, owner, stream, eventID string, newID func() string, now time.Time) (Issued, error) {
	r := delivery.Receipt{EventID: eventID, ReceiptID: newID(), At: now.UTC()}
	res, err := db.ExecContext(ctx, s.Dialect.Q(`INSERT INTO delivery_issued (event_id, receipt_id, owner, stream, issued_at)
VALUES (?, ?, ?, ?, ?) ON CONFLICT (event_id) DO NOTHING`), eventID, r.ReceiptID, owner, stream, s.Dialect.Time(r.At))
	if err != nil {
		return Issued{}, fmt.Errorf("makbuz yazılamadı: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 1 {
		return Issued{Receipt: r, New: true}, nil
	}
	// The event was processed before: the same receipt id.
	var at time.Time
	if err := db.QueryRowContext(ctx, s.Dialect.Q(`SELECT receipt_id, issued_at FROM delivery_issued WHERE event_id = ?`), eventID).
		Scan(&r.ReceiptID, sqldialect.ScanTime(&at)); err != nil {
		return Issued{}, fmt.Errorf("makbuz okunamadı: %w", err)
	}
	r.At = at
	return Issued{Receipt: r}, nil
}

// SaveReceived writes the receipt the sender received next to the event. If the same event's
// receipt comes again the first is valid (the first value is false).
func (s Store) SaveReceived(ctx context.Context, db sqldialect.DB, r delivery.Receipt, at time.Time) (bool, error) {
	res, err := db.ExecContext(ctx, s.Dialect.Q(`INSERT INTO delivery_received (event_id, receipt_id, issued_at, received_at)
VALUES (?, ?, ?, ?) ON CONFLICT (event_id) DO NOTHING`), r.EventID, r.ReceiptID, s.Dialect.Time(r.At), s.Dialect.Time(at))
	if err != nil {
		return false, fmt.Errorf("makbuz kaydedilemedi: %w", err)
	}
	n, err := res.RowsAffected()
	return err == nil && n == 1, err
}

// Received returns, of the given event ids, those whose receipt is recorded.
func (s Store) Received(ctx context.Context, db sqldialect.DB, eventIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(eventIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(eventIDs))
	for i, id := range eventIDs {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, s.Dialect.Q(`SELECT event_id FROM delivery_received WHERE event_id IN (?`+
		strings.Repeat(", ?", len(eventIDs)-1)+`)`), args...)
	if err != nil {
		return nil, fmt.Errorf("makbuzlar okunamadı: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// PruneIssued removes at most limit given receipts older than before and returns how many it
// removed. A given receipt answers an event that comes again while it is still in the
// sender's stream; an older event that comes again gets a new receipt id, which the sender
// takes the same way.
func (s Store) PruneIssued(ctx context.Context, db sqldialect.DB, before time.Time, limit int) (int64, error) {
	return s.prune(ctx, db, "delivery_issued", "issued_at", before, limit)
}

// PruneReceived removes at most limit received receipts older than before and returns how
// many it removed. The stream cleaner needs a receipt until it has deleted the event
// (retention.Config.CheckReceipts).
func (s Store) PruneReceived(ctx context.Context, db sqldialect.DB, before time.Time, limit int) (int64, error) {
	return s.prune(ctx, db, "delivery_received", "received_at", before, limit)
}

func (s Store) prune(ctx context.Context, db sqldialect.DB, table, column string, before time.Time, limit int) (int64, error) {
	// The receipt ids are UUIDv7, ordered by time: along their index the old rows come first.
	res, err := db.ExecContext(ctx, s.Dialect.Q(`DELETE FROM `+table+` WHERE event_id IN (SELECT event_id FROM `+table+
		` WHERE `+column+` < ? ORDER BY receipt_id LIMIT ?)`), s.Dialect.Time(before), limit)
	if err != nil {
		return 0, fmt.Errorf("receipts could not be pruned: %w", err)
	}
	return res.RowsAffected()
}
