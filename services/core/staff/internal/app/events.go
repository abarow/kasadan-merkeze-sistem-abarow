// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"time"

	"abarow.com/services/core/staff/internal/domain"
)

// ChargedSales counts the sales whose payment was taken but whose fiscal record (receipt)
// could not be made (sales).
type ChargedSales interface {
	CountCharged(ctx context.Context) (int64, error)
}

// EventBacklog is a source of sale events not yet processed by staff
// (e.g. sales' outbox, staff's NATS consumer).
type EventBacklog interface {
	// PendingEvents returns the number of events waiting in this source.
	PendingEvents(ctx context.Context) (int64, error)
}

// Events waits for the sale events to be processed by staff before the end of day and the shift
// closing; so the report includes every sale made up to this moment.
//
// The sources are checked in order: first the events sales has not published, then those
// the staff consumer has not processed. Zero at sales shows that every event up to that
// moment is on the message path; then zero at the consumer shows that all were processed.
// If the order were reversed an event published in between could slip by.
type Events struct {
	// If Charged is given it is first checked that there is no sale whose receipt could not be
	// printed: if there is, the report would be incomplete and the cash in the drawer would look excessive.
	Charged ChargedSales
	Sources []EventBacklog
	// Timeout is the longest time to wait; if it is up domain.EventsPendingError is
	// returned.
	Timeout time.Duration
	// Interval is the wait between checks.
	Interval time.Duration
}

// WaitProcessed returns domain.UnfinishedSalesError if there is a sale whose receipt could not
// be printed; otherwise it waits until the pending events in the sources are processed.
// If there are no sources it returns immediately.
func (e Events) WaitProcessed(ctx context.Context) error {
	if e.Charged != nil {
		n, err := e.Charged.CountCharged(ctx)
		if err != nil {
			return fmt.Errorf("fişi basılamamış satışlar sayılamadı: %w", err)
		}
		if n > 0 {
			return domain.UnfinishedSalesError{Count: n}
		}
	}
	deadline := time.Now().Add(e.Timeout)
	for _, src := range e.Sources {
		for {
			n, err := src.PendingEvents(ctx)
			if err == nil && n == 0 {
				break
			}
			if !time.Now().Before(deadline) {
				return domain.EventsPendingError{Pending: n, Err: err}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.Interval):
			}
		}
	}
	return nil
}
