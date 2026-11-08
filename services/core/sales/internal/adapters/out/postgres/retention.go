// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"time"

	"abarow.com/pkg/retention"
)

// pruneBatch is the number of rows removed in one statement: the service has a single
// connection, a sale waits only for a short step.
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

// PruneSales removes the finished (paid or voided) sales whose last change is older than
// before, with their lines and payments. An open or charged sale is never removed.
// Nothing is removed while an event that occurred before then has no receipt from the
// store server: until then the register's copy is the only one. The receipt numbers come
// from receipt_counter, so removing sales does not change the next number.
func (s *Store) PruneSales(ctx context.Context, before time.Time) (int64, error) {
	if u, err := outboxTable.Undelivered(ctx, s.db, before); err != nil {
		return 0, err
	} else if u {
		return 0, fmt.Errorf("%w: an event older than %s has no receipt from the store server yet",
			retention.ErrSkipped, before.Format(time.DateOnly))
	}
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		// The lines and payments go with ON DELETE CASCADE.
		res, err := s.db.ExecContext(ctx, `DELETE FROM sales WHERE id IN (SELECT id FROM sales
			WHERE status IN ('paid', 'voided') AND GREATEST(created_at, paid_at, voided_at) < $1 LIMIT $2)`, before, limit)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	})
}
