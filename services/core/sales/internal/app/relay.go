// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"time"

	"abarow.com/pkg/outbox"
)

// The outbox relay is in the shared package (pkg/outbox; staff uses it too). sales uses it
// under its own names.
type (
	// PendingMessage is an outbox message that has not been published yet.
	PendingMessage = outbox.Pending
	// Outbox gives the messages waiting to be published.
	Outbox = outbox.Store
	// Publisher publishes the events to the message path (NATS JetStream).
	Publisher = outbox.Publisher
	// Relay publishes the messages in the outbox in the order they were written.
	Relay = outbox.Relay
)

// NewRelay returns a relay that publishes the outbox at the given interval.
func NewRelay(o Outbox, pub Publisher, interval time.Duration, log *slog.Logger) *Relay {
	return outbox.NewRelay(o, pub, interval, log)
}
