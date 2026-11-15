// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsbacklog is the app.EventBacklog adapter that counts the sale events
// staff's NATS consumer has not processed yet.
package natsbacklog

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/services/core/staff/internal/adapters/in/natsconsumer"
	"abarow.com/services/core/staff/internal/app"
)

// Backlog counts the pending events in the durable consumer: those not yet delivered
// and those delivered but not acknowledged (being processed).
type Backlog struct {
	JS jetstream.JetStream
}

var _ app.EventBacklog = Backlog{}

func (b Backlog) PendingEvents(ctx context.Context) (int64, error) {
	cons, err := b.JS.Consumer(ctx, natsconsumer.Stream, natsconsumer.Durable)
	// If there is no stream no sale event has ever been published.
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	info, err := cons.Info(ctx)
	if err != nil {
		return 0, err
	}
	return int64(info.NumPending) + int64(info.NumAckPending), nil
}
