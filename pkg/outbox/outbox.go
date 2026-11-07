// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package outbox is for services to pass events safely to the message path (NATS
// JetStream). The service writes its own data and the event to be published in the
// same database transaction (the outbox table); Relay publishes them in the order they
// were written. So the situation "the record was made but the event did not go" cannot
// arise. sales and staff use it.
package outbox

import (
	"context"
	"log/slog"
	"time"
)

// Message is an event written to the outbox and later passed on to the message path.
type Message struct {
	// ID is the event's unique id; the consumers de-duplicate repeats with it.
	ID string
	// Type, CloudEvents tipidir: com.abarow.sale.completed.v1.
	Type string
	// Subject, NATS subject'idir: sale.completed.v1.
	Subject    string
	StoreID    string
	RegisterID string
	// Payload is the encoded form of the event.
	Payload []byte
	// ContentType is the content type of the Payload (application/protobuf or, in old
	// records, application/json).
	ContentType string
	OccurredAt  time.Time
	// Seq and Producer are filled in at publish time by Relay.Sequenced: the event's
	// sequential number in the sender outbox and the name of that outbox. If the receiver sees a
	// missing number in between there is a lost event (pkg/sequence). They are empty
	// at write time.
	Seq      int64
	Producer string
}

// Pending is an outbox message that has not been published yet.
type Pending struct {
	Seq int64
	Message
}

// Store gives the messages waiting to be published.
type Store interface {
	// Pending returns the unpublished messages in the order they were written.
	Pending(ctx context.Context, limit int) ([]Pending, error)
	// MarkPublished marks the message as published.
	MarkPublished(ctx context.Context, seq int64, at time.Time) error
	// CountPending counts the unpublished messages.
	CountPending(ctx context.Context) (int64, error)
}

// Publisher publishes the events to the message path.
type Publisher interface {
	// Publish returns when the message is stored permanently. The same message can be
	// published again; the message path de-duplicates repeats by the message ID.
	Publish(ctx context.Context, m Message) error
}

// Relay publishes the messages in the outbox in the order they were written.
//
// It delivers at least once: a message that was published but could not be marked is
// published again in the next round; consumers de-duplicate repeats by the message ID.
// While the message path cannot be reached the service keeps working, the messages
// accumulate in the outbox.
type Relay struct {
	store    Store
	pub      Publisher
	interval time.Duration
	batch    int
	now      func() time.Time
	log      *slog.Logger
	producer string
}

// NewRelay returns a relay that publishes the outbox at the given interval.
func NewRelay(store Store, pub Publisher, interval time.Duration, log *slog.Logger) *Relay {
	return &Relay{store: store, pub: pub, interval: interval, batch: 100, now: time.Now, log: log}
}

// Sequenced adds the outbox sequence number (seq) and the outbox's name (producer) to
// every published message; if the receiver sees a missing number in between headquarters
// understands it (pkg/sequence). It is used only on an outbox whose numbers are given
// without gaps: SQLite AUTOINCREMENT does not consume a number in a rolled-back transaction;
// PostgreSQL BIGSERIAL does, and it would raise a false alarm.
func (r *Relay) Sequenced(producer string) *Relay {
	r.producer = producer
	return r
}

// Run empties the outbox at intervals until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	failing := false
	for {
		n, err := r.Flush(ctx)
		switch {
		case err != nil && !failing:
			r.log.Warn("outbox could not be published; messages are being held", "err", err, "published", n)
			failing = true
		case err == nil && failing:
			r.log.Info("outbox publishing works again", "published", n)
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Backlog returns the number of events not yet published. A message counts as published
// only after the message path has stored it permanently; zero means that every event
// written up to this moment is on the message path.
func (r *Relay) Backlog(ctx context.Context) (int64, error) { return r.store.CountPending(ctx) }

// Flush publishes the pending messages in order and returns how many messages were
// published. If a message cannot be published it stops so that the order is not broken.
func (r *Relay) Flush(ctx context.Context) (int, error) {
	n := 0
	for {
		msgs, err := r.store.Pending(ctx, r.batch)
		if err != nil || len(msgs) == 0 {
			return n, err
		}
		for _, m := range msgs {
			msg := m.Message
			if r.producer != "" {
				msg.Seq, msg.Producer = m.Seq, r.producer
			}
			if err := r.pub.Publish(ctx, msg); err != nil {
				return n, err
			}
			if err := r.store.MarkPublished(ctx, m.Seq, r.now()); err != nil {
				return n, err
			}
			n++
		}
	}
}
