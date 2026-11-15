// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"abarow.com/pkg/retention"
)

// pruneBatch is the number of rows removed in one statement: the service has a single
// connection, the requests wait only for a short step.
const pruneBatch = 1000

// PruneOutbox removes the published events whose receipt the store server gave.
func (s *Store) PruneOutbox(ctx context.Context, before time.Time) (int64, error) {
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return outboxTable.PruneDelivered(ctx, s.db, before, limit)
	})
}

// PruneReceipts removes the received receipts older than before.
func (s *Store) PruneReceipts(ctx context.Context, before time.Time) (int64, error) {
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return deliveryStore.PruneReceived(ctx, s.db, before, limit)
	})
}

// PruneDays removes the details of the periods (days) closed before the given moment: the
// sales the reports are made of, and the cash movements and drawer openings of the shifts
// closed in those days. The period and shift rows stay with details_removed_at, so their
// reports say the details are gone instead of showing zero. It also removes the expired
// manager approvals. Nothing is removed while an event of the register that occurred
// before then has no receipt from the store server: until then the register's copy is
// the only one.
//
// A day is closed only when no shift is open (CloseDay), so every shift lies inside one
// period; the days are handled one by one, oldest first, each in its own transaction.
func (s *Store) PruneDays(ctx context.Context, before time.Time) (int64, error) {
	if u, err := outboxTable.Undelivered(ctx, s.db, before); err != nil {
		return 0, err
	} else if u {
		return 0, fmt.Errorf("%w: an event older than %s has no receipt from the store server yet",
			retention.ErrSkipped, before.Format(time.DateOnly))
	}
	cutoff := formatTime(before)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var zNo int64
		var closedAt string
		err := s.db.QueryRowContext(ctx, `SELECT z_no, closed_at FROM periods
			WHERE closed_at < $1 AND details_removed_at IS NULL ORDER BY z_no LIMIT 1`, cutoff).Scan(&zNo, &closedAt)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return total, err
		}
		var n int64
		err = s.inTx(ctx, func(tx *sql.Tx) error {
			now := formatTime(time.Now())
			for _, q := range []string{
				`DELETE FROM completed_sales WHERE z_no = $1`,
				`DELETE FROM voided_sales WHERE z_no = $1`,
			} {
				res, err := tx.ExecContext(ctx, q, zNo)
				if err != nil {
					return err
				}
				k, _ := res.RowsAffected()
				n += k
			}
			// The shifts of this day: closed by its Z and not handled yet (the older days
			// came first).
			for _, q := range []string{
				`DELETE FROM cash_movements WHERE shift_id IN (SELECT id FROM shifts WHERE closed_at <= $1 AND details_removed_at IS NULL)`,
				`DELETE FROM drawer_opens WHERE shift_id IN (SELECT id FROM shifts WHERE closed_at <= $1 AND details_removed_at IS NULL)`,
			} {
				res, err := tx.ExecContext(ctx, q, closedAt)
				if err != nil {
					return err
				}
				k, _ := res.RowsAffected()
				n += k
			}
			if _, err := tx.ExecContext(ctx, `UPDATE shifts SET details_removed_at = $1 WHERE closed_at <= $2 AND details_removed_at IS NULL`, now, closedAt); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE periods SET details_removed_at = $1 WHERE z_no = $2`, now, zNo)
			return err
		})
		if err != nil {
			return total, fmt.Errorf("the details of Z %d could not be removed: %w", zNo, err)
		}
		total += n
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM approvals WHERE expires_at < $1`, cutoff)
	if err != nil {
		return total, err
	}
	n, _ := res.RowsAffected()
	return total + n, nil
}
