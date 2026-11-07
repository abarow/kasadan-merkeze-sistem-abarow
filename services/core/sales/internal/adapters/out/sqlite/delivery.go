// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/delivery/deliverydb"
	"abarow.com/pkg/sqldialect"
)

var deliveryStore = deliverydb.Store{Dialect: sqldialect.SQLite}

// SaveReceived writes the receipt given by the store server next to the event (a receipt
// that comes a second time is ignored).
func (s *Store) SaveReceived(ctx context.Context, r delivery.Receipt) error {
	_, err := deliveryStore.SaveReceived(ctx, s.db, r, time.Now())
	return err
}

// Received returns, of the given event ids, those whose receipt is recorded.
func (s *Store) Received(ctx context.Context, eventIDs []string) (map[string]bool, error) {
	return deliveryStore.Received(ctx, s.db, eventIDs)
}
