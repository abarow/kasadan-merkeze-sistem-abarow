// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/delivery/deliverydb"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/sqldialect"
)

// receiptOutbox is the outbox table of the receipts given to the registers (its SQL is in
// pkg/outbox).
var receiptOutbox = outbox.Table{Name: "receipt_outbox", Dialect: sqldialect.Postgres, Store: true}

var deliveryStore = deliverydb.Store{Dialect: sqldialect.Postgres}

// IssueReceipt gives the receipt that the event coming from a register has been processed into the database
// and writes the receipt to the outbox to be published to the register (pkg/delivery). owner is the id
// of the register that sent the event, the stream is its stream. If a receipt has already
// been given for the event it writes nothing. It is called after the event is recorded: since the message
// is acknowledged only when both are done, if it crashes in between the message comes again
// and the receipt is completed.
func (s *Store) IssueReceipt(ctx context.Context, owner, stream, eventID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := deliveryStore.Issue(ctx, tx, owner, stream, eventID, s.NewID, time.Now())
		if err != nil || !r.New {
			return err
		}
		msg, err := delivery.OutboxMessage(owner, stream, r.Receipt)
		if err != nil {
			return err
		}
		msg.StoreID = s.StoreID
		return receiptOutbox.Enqueue(ctx, tx, msg)
	})
}

// SaveReceived writes the receipt given by headquarters next to the event (a receipt that comes
// a second time is ignored).
func (s *Store) SaveReceived(ctx context.Context, r delivery.Receipt) error {
	_, err := deliveryStore.SaveReceived(ctx, s.db, r, time.Now())
	return err
}

// Received returns, of the given event ids, those whose headquarters receipt is recorded.
func (s *Store) Received(ctx context.Context, eventIDs []string) (map[string]bool, error) {
	return deliveryStore.Received(ctx, s.db, eventIDs)
}

// ReceiptOutbox is the outbox of the receipts given to the registers (outbox.Store);
// it is published to the receipt stream with its own relay.
type ReceiptOutbox struct{ s *Store }

var _ outbox.Store = ReceiptOutbox{}

// Receipts returns the receipt outbox.
func (s *Store) Receipts() ReceiptOutbox { return ReceiptOutbox{s} }

// Pending returns the unpublished receipts in the order they were written.
func (o ReceiptOutbox) Pending(ctx context.Context, limit int) ([]outbox.Pending, error) {
	return receiptOutbox.Pending(ctx, o.s.db, limit)
}

// MarkPublished marks the receipt as published.
func (o ReceiptOutbox) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return receiptOutbox.MarkPublished(ctx, o.s.db, seq, at)
}

// CountPending counts the unpublished receipts.
func (o ReceiptOutbox) CountPending(ctx context.Context) (int64, error) {
	return receiptOutbox.CountPending(ctx, o.s.db)
}
