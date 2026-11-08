// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"time"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
)

// pruneBatch is the number of rows removed in one statement.
const pruneBatch = 5000

// PruneOutbox removes the messages published before the given moment from both outboxes
// (the store's own events and the receipts given to the registers). The store stream
// keeps a published event until headquarters' receipt comes.
func (s *Store) PruneOutbox(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for _, tb := range []outbox.Table{outboxTable, receiptOutbox} {
		n, err := retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
			return tb.Prune(ctx, s.db, before, limit)
		})
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// PruneReceipts removes the receipts given to the registers and the receipts received
// from headquarters before the given moment.
func (s *Store) PruneReceipts(ctx context.Context, before time.Time) (int64, error) {
	issued, err := retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return deliveryStore.PruneIssued(ctx, s.db, before, limit)
	})
	if err != nil {
		return issued, err
	}
	received, err := retention.Batches(ctx, pruneBatch, func(ctx context.Context, limit int) (int64, error) {
		return deliveryStore.PruneReceived(ctx, s.db, before, limit)
	})
	return issued + received, err
}
