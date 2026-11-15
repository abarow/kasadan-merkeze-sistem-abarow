// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Sender is the receipt side of a tier (register or store server) that sends events to
// a tier above: it sets up the receipt stream, writes the tier above's receipts to its own
// database and deletes the old events in its stream only if proven by their
// receipt.
type Sender struct {
	JS jetstream.JetStream
	// Owner is this tier's id (the register or store id); the receipts come with this
	// id.
	Owner string
	// Source is the JetStream domain of the tier above that gives the receipts. If empty there is no
	// tier above (the store server or headquarters is not installed): no receipt comes and no
	// event is deleted.
	Source string
	// Stream is its own stream whose events will be deleted (e.g. ABAROW_SALES).
	Stream string
	// Durable is the receipt consumer's name; it must be unique among the services
	// sharing the same NATS.
	Durable string
	// Save writes the receipt to the database (deliverydb.Store.SaveReceived).
	Save func(ctx context.Context, r Receipt) error
	// Received returns the event ids whose receipt is recorded (deliverydb.Store.Received).
	Received func(ctx context.Context, eventIDs []string) (map[string]bool, error)
	// DefaultMaxAge is the least time an event is kept if ABAROW_CLEAN_MAX_AGE is not given
	// (30 days at the register, 90 days at the store server).
	DefaultMaxAge time.Duration
}

// Start starts the receipt scheme; the background jobs stop when ctx ends.
// It returns an error if the environment variables are invalid (the service does not start with a wrong setting).
// The receipt stream is set up even if Source is empty: the receipts of the tiers
// below are written to the same stream.
func (s Sender) Start(ctx context.Context, getenv func(string) string, log *slog.Logger) error {
	clean, err := CleanConfigFromEnv(getenv, s.DefaultMaxAge)
	if err != nil {
		return err
	}
	receiptAge, err := ReceiptMaxAgeFromEnv(getenv)
	if err != nil {
		return err
	}
	go EnsureStream(ctx, s.JS, ReceiptConfig(s.Source, s.Owner, receiptAge), log)
	if s.Source == "" {
		log.Info("the tier above (store server or headquarters) is not defined: no event is deleted from the stream", "stream", s.Stream)
		return nil
	}
	go NewReceiptConsumer(s.JS, s.Durable, Subject(s.Owner, s.Stream), s.Save, log).Run(ctx)
	go RunCleaner(ctx, s.JS, s.Stream, s.Received, clean, log)
	return nil
}
