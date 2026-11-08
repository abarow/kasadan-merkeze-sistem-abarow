// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"time"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/sqldialect"
)

// outboxTable is the outbox table of the events (its SQL is in pkg/outbox).
var outboxTable = outbox.Table{Name: "outbox", Dialect: sqldialect.Postgres, Store: true, Register: true}

// Store keeps staff's events (shift) in the outbox too; the relay passes them on to the
// message path.
var _ outbox.Store = (*Store)(nil)

// appendOutbox writes the event to the outbox; the caller calls it in the same
// transaction as its own record.
func appendOutbox(ctx context.Context, tx *sql.Tx, m outbox.Message) error {
	return outboxTable.Enqueue(ctx, tx, m)
}

// Pending returns the unpublished outbox messages in the order they were written.
func (s *Store) Pending(ctx context.Context, limit int) ([]outbox.Pending, error) {
	return outboxTable.Pending(ctx, s.db, limit)
}

// MarkPublished marks the outbox message as published.
func (s *Store) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return outboxTable.MarkPublished(ctx, s.db, seq, at)
}

// CountPending counts the unpublished outbox messages.
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	return outboxTable.CountPending(ctx, s.db)
}
