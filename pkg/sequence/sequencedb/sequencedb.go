// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sequencedb stores the sequence numbers of received events as ranges
// (pkg/sequence): for each (store, register, sender) the ranges "from these numbers to this
// one, all have come". A gap in between is a missing event. Since the ranges
// merge, the number of rows depends not on the number of events but on the number missing.
//
// Table (in each service's migration; see the testdata/ schemas):
// event_ranges (store_id, register_id, producer, from_seq, to_seq).
// Record is not called concurrently for the same key (the consumers process the events
// in order).
package sequencedb

import (
	"context"
	"database/sql"

	"abarow.com/pkg/sequence"
	"abarow.com/pkg/sqldialect"
)

// Tracker keeps the ranges in a database.
type Tracker struct {
	DB      *sql.DB
	Dialect sqldialect.Dialect
}

// Record writes that the event numbered seq has come for (store, register, sender).
// If the same number comes a second time (events come at least once) it changes
// nothing. If the number is adjacent to a neighboring range it extends it, and may
// merge two ranges.
func (t Tracker) Record(ctx context.Context, store, register, producer string, seq int64) error {
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := t.Dialect.Q

	// The most common case: the number comes right after the last range.
	res, err := tx.ExecContext(ctx, q(`UPDATE event_ranges SET to_seq = ? WHERE store_id = ? AND register_id = ? AND producer = ? AND to_seq = ?`),
		seq, store, register, producer, seq-1)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		// The range was extended; if another range starts right after it, merge them.
		var next int64
		err := tx.QueryRowContext(ctx, q(`DELETE FROM event_ranges WHERE store_id = ? AND register_id = ? AND producer = ? AND from_seq = ? RETURNING to_seq`),
			store, register, producer, seq+1).Scan(&next)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return err
		default:
			if _, err := tx.ExecContext(ctx, q(`UPDATE event_ranges SET to_seq = ? WHERE store_id = ? AND register_id = ? AND producer = ? AND to_seq = ?`),
				next, store, register, producer, seq); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	// Is it already inside a range (a repeat)?
	var one int
	err = tx.QueryRowContext(ctx, q(`SELECT 1 FROM event_ranges WHERE store_id = ? AND register_id = ? AND producer = ? AND from_seq <= ? AND to_seq >= ?`),
		store, register, producer, seq, seq).Scan(&one)
	switch {
	case err == nil:
		return tx.Commit()
	case err != sql.ErrNoRows:
		return err
	}

	// If there is an adjacent range on the right extend it to the left; otherwise a new range.
	var next int64
	err = tx.QueryRowContext(ctx, q(`DELETE FROM event_ranges WHERE store_id = ? AND register_id = ? AND producer = ? AND from_seq = ? RETURNING to_seq`),
		store, register, producer, seq+1).Scan(&next)
	switch {
	case err == sql.ErrNoRows:
		next = seq
	case err != nil:
		return err
	}
	if _, err := tx.ExecContext(ctx, q(`INSERT INTO event_ranges (store_id, register_id, producer, from_seq, to_seq) VALUES (?, ?, ?, ?, ?)`),
		store, register, producer, seq, next); err != nil {
		return err
	}
	return tx.Commit()
}

// Gaps returns the missing number ranges by register, sender and number.
// All stores if store is empty; if limit is greater than zero at most that many ranges.
func (t Tracker) Gaps(ctx context.Context, store string, limit int) ([]sequence.Gap, error) {
	query := `SELECT store_id, register_id, producer, to_seq + 1, next_from - 1 FROM (
		SELECT store_id, register_id, producer, to_seq,
		       LEAD(from_seq) OVER (PARTITION BY store_id, register_id, producer ORDER BY from_seq) AS next_from
		FROM event_ranges WHERE (? = '' OR store_id = ?)
	) AS r WHERE next_from IS NOT NULL ORDER BY store_id, register_id, producer, 4`
	args := []any{store, store}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := t.DB.QueryContext(ctx, t.Dialect.Q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sequence.Gap
	for rows.Next() {
		var g sequence.Gap
		if err := rows.Scan(&g.Store, &g.Register, &g.Producer, &g.From, &g.To); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Last is the largest number that came from a sender (0 if none ever came).
func (t Tracker) Last(ctx context.Context, store, register, producer string) (int64, error) {
	var n sql.NullInt64
	err := t.DB.QueryRowContext(ctx, t.Dialect.Q(`SELECT max(to_seq) FROM event_ranges WHERE store_id = ? AND register_id = ? AND producer = ?`),
		store, register, producer).Scan(&n)
	return n.Int64, err
}
