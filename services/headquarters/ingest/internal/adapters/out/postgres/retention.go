// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"time"

	"abarow.com/pkg/delivery/deliverydb"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/sqldialect"
)

// pruneBatch is the number of rows changed in one statement.
const pruneBatch = 5000

// PruneEventPayloads empties the raw content of the events received before the given
// moment. The sales ledger keeps the processed form of every event; the raw content
// would only be needed to process old events again.
func (s *Store) PruneEventPayloads(ctx context.Context, before time.Time) (int64, error) {
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		res, err := s.db.ExecContext(ctx, `UPDATE events SET payload = NULL WHERE id IN (SELECT id FROM events
			WHERE received_at < $1 AND payload IS NOT NULL ORDER BY received_at LIMIT $2)`, before, limit)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	})
}

// PruneReceipts removes the receipts given before the given moment.
func (s *Store) PruneReceipts(ctx context.Context, before time.Time) (int64, error) {
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return deliverydb.Store{Dialect: sqldialect.Postgres}.PruneIssued(ctx, s.db, before, limit)
	})
}

// PruneOutbox removes the receipts published to the stores before the given moment.
func (s *Store) PruneOutbox(ctx context.Context, before time.Time) (int64, error) {
	return retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return receiptOutbox.Prune(ctx, s.db, before, limit)
	})
}
