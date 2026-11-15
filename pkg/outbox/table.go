// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/sqldialect"
)

// Table is a service's outbox table: the SQL that writes the message, reads the
// pending ones and marks them published is written once here (SQLite and
// PostgreSQL). The table always has seq (increasing), id, type, subject, payload,
// content_type, occurred_at and published_at (NULL if not published);
// the store_id and register_id columns are optional.
type Table struct {
	// Name is the table's name (e.g. outbox, stock_outbox).
	Name    string
	Dialect sqldialect.Dialect
	// Store and Register say the table has the store_id and register_id
	// columns.
	Store, Register bool
	// StoreID is the store id of the messages read if there is no store_id column
	// (e.g. headquarters).
	StoreID string
}

func (t Table) columns() []string {
	cols := []string{"id", "type", "subject"}
	if t.Store {
		cols = append(cols, "store_id")
	}
	if t.Register {
		cols = append(cols, "register_id")
	}
	return append(cols, "payload", "content_type", "occurred_at")
}

// Enqueue writes the messages to the outbox; the caller calls it in the same
// transaction as its own record (db is a *sql.Tx).
func (t Table) Enqueue(ctx context.Context, db sqldialect.DB, msgs ...Message) error {
	cols := t.columns()
	query := t.Dialect.Q("INSERT INTO " + t.Name + " (" + strings.Join(cols, ", ") + ") VALUES (?" +
		strings.Repeat(", ?", len(cols)-1) + ")")
	for _, m := range msgs {
		args := []any{m.ID, m.Type, m.Subject}
		if t.Store {
			args = append(args, m.StoreID)
		}
		if t.Register {
			args = append(args, m.RegisterID)
		}
		args = append(args, m.Payload, m.ContentType, t.Dialect.Time(m.OccurredAt))
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("outbox'a yazılamadı: %w", err)
		}
	}
	return nil
}

// Pending returns the unpublished messages in the order they were written.
func (t Table) Pending(ctx context.Context, db sqldialect.DB, limit int) ([]Pending, error) {
	rows, err := db.QueryContext(ctx, t.Dialect.Q("SELECT seq, "+strings.Join(t.columns(), ", ")+" FROM "+t.Name+
		" WHERE published_at IS NULL ORDER BY seq LIMIT ?"), limit)
	if err != nil {
		return nil, fmt.Errorf("outbox okunamadı: %w", err)
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		p := Pending{Message: Message{StoreID: t.StoreID}}
		dest := []any{&p.Seq, &p.ID, &p.Type, &p.Subject}
		if t.Store {
			dest = append(dest, &p.StoreID)
		}
		if t.Register {
			dest = append(dest, &p.RegisterID)
		}
		dest = append(dest, &p.Payload, &p.ContentType, sqldialect.ScanTime(&p.OccurredAt))
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("outbox okunamadı: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPublished marks the message as published.
func (t Table) MarkPublished(ctx context.Context, db sqldialect.DB, seq int64, at time.Time) error {
	if _, err := db.ExecContext(ctx, t.Dialect.Q("UPDATE "+t.Name+" SET published_at = ? WHERE seq = ?"), t.Dialect.Time(at), seq); err != nil {
		return fmt.Errorf("outbox işaretlenemedi: %w", err)
	}
	return nil
}

// Prune removes at most limit messages published before the given moment and returns how
// many it removed. A published message is on the message path, which keeps it until its
// receipt comes (pkg/delivery); nothing reads the published rows. The numbers come from
// counters (or a sequence), so removing rows does not change the next number.
func (t Table) Prune(ctx context.Context, db sqldialect.DB, before time.Time, limit int) (int64, error) {
	return t.prune(ctx, db, "", before, limit)
}

// PruneDelivered is Prune for an outbox whose events get receipts: a message is removed
// only if its receipt is recorded in delivery_received as well. A message left in the
// table after that is an event the tier above has not confirmed yet.
func (t Table) PruneDelivered(ctx context.Context, db sqldialect.DB, before time.Time, limit int) (int64, error) {
	return t.prune(ctx, db, " AND EXISTS (SELECT 1 FROM delivery_received r WHERE r.event_id = "+t.Name+".id)", before, limit)
}

// Undelivered says whether a message that occurred before the given moment has no receipt
// recorded in delivery_received yet (published or not). With PruneDelivered every
// confirmed message leaves the table, so this looks only at the few rows left.
func (t Table) Undelivered(ctx context.Context, db sqldialect.DB, before time.Time) (bool, error) {
	var found bool
	err := db.QueryRowContext(ctx, t.Dialect.Q("SELECT EXISTS (SELECT 1 FROM "+t.Name+" WHERE occurred_at < ?"+
		" AND NOT EXISTS (SELECT 1 FROM delivery_received r WHERE r.event_id = "+t.Name+".id))"), t.Dialect.Time(before)).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("outbox could not be checked: %w", err)
	}
	return found, nil
}

func (t Table) prune(ctx context.Context, db sqldialect.DB, also string, before time.Time, limit int) (int64, error) {
	// Oldest first along the primary key: the messages were published in seq order.
	res, err := db.ExecContext(ctx, t.Dialect.Q("DELETE FROM "+t.Name+" WHERE seq IN (SELECT seq FROM "+t.Name+
		" WHERE published_at < ?"+also+" ORDER BY seq LIMIT ?)"), t.Dialect.Time(before), limit)
	if err != nil {
		return 0, fmt.Errorf("outbox could not be pruned: %w", err)
	}
	return res.RowsAffected()
}

// CountPending counts the unpublished messages.
func (t Table) CountPending(ctx context.Context, db sqldialect.DB) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+t.Name+" WHERE published_at IS NULL").Scan(&n); err != nil {
		return 0, fmt.Errorf("outbox sayılamadı: %w", err)
	}
	return n, nil
}
