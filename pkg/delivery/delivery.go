// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package delivery is the receipt scheme that proves that a tier's event has been processed
// into the database of the tier above; an event with no recorded receipt
// is not deleted.
//
// Events flow from the register to the store server and from there to headquarters through
// source streams. NATS's delivery information (the acknowledgement of the source consumer)
// says only "the receiving stream wrote it"; it does not say that it was processed into the
// database and it is not visible on the sender's side. So the scheme is at the application level:
//
//   - In the transaction in which the receiver processes the event into the database it produces a receipt
//     id (UUIDv7) for that event and writes it to its own database (deliverydb.Issue), and
//     puts the receipt in the outbox in the same transaction; the receipt goes back down to the sender
//     through the outbox. If the same event comes again the same receipt id is used.
//   - The sender writes the receipt next to the event in its own database
//     (deliverydb.SaveReceived) and acknowledges only after that.
//   - The sender deletes an old event in its stream only if the receipt id is recorded
//     in its own database (Cleaner). Without a receipt the event is not deleted:
//     if there is no receiver (headquarters is not installed), it cannot be reached or it has
//     not processed it yet, the event stays.
//
// It need not be instant: the receipt may come minutes later, the event stays until then.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"abarow.com/pkg/outbox"
)

// ReceiptType is the kind of the receipt message (the outbox and event type).
const ReceiptType = "receipt.issued.v1"

// Receipt says that an event has been processed into the receiver's database.
type Receipt struct {
	// EventID is the id of the processed event (ce-id).
	EventID string `json:"event_id"`
	// ReceiptID is the UUIDv7 the receiver produced for that event; it stays next to the event
	// in the receiver's database.
	ReceiptID string `json:"receipt_id"`
	// At is the moment the receipt was given.
	At time.Time `json:"at"`
}

// Marshal turns the receipt into the content to be published.
func (r Receipt) Marshal() ([]byte, error) { return json.Marshal(r) }

// ParseReceipt reads a published receipt.
func ParseReceipt(b []byte) (Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(b, &r); err != nil {
		return Receipt{}, fmt.Errorf("makbuz okunamadı: %w", err)
	}
	if r.EventID == "" || r.ReceiptID == "" {
		return Receipt{}, errors.New("makbuzda olay ya da makbuz kimliği yok")
	}
	return r, nil
}

// NewID is a new receipt id: UUIDv7 (ordered by time).
func NewID() string { return uuid.Must(uuid.NewV7()).String() }

// Subject is the receipt's subject: "receipt.<owner>.<stream>"; owner is the sender
// of the event (the store or register id), stream is its stream. The ids
// are subject tokens: they contain no dot, space or wildcard.
func Subject(owner, stream string) string {
	return "receipt." + owner + "." + strings.ToLower(stream)
}

// OutboxMessage turns the receipt into a message to be written to the receiver's outbox.
func OutboxMessage(owner, stream string, r Receipt) (outbox.Message, error) {
	b, err := r.Marshal()
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: r.ReceiptID, Type: ReceiptType, Subject: Subject(owner, stream),
		Payload: b, ContentType: "application/json", OccurredAt: r.At,
	}, nil
}

// Origin reads the origin stream's name from the header (Nats-Stream-Source) of a message
// that came through a source; ok is false if the message did not come from a source.
// The header's format is "<stream> <seq> <filter> <subject>".
func Origin(h nats.Header) (stream string, ok bool) {
	f := strings.Fields(h.Get("Nats-Stream-Source"))
	if len(f) < 2 {
		return "", false
	}
	if n, err := strconv.ParseUint(f[1], 10, 64); err != nil || n == 0 {
		return "", false
	}
	// In a message coming from another JetStream domain (leaf) the domain's digest is added to
	// the end of the stream name ("ABAROW_SALES:jSqTWyQ6"); a stream name has no ':'.
	stream, _, _ = strings.Cut(f[0], ":")
	return stream, stream != ""
}

// EventID is the message's event id: ce-id, otherwise Nats-Msg-Id. A message with
// no id cannot be given a receipt, so it is not deleted.
func EventID(h nats.Header) string {
	if id := h.Get("ce-id"); id != "" {
		return id
	}
	return h.Get(nats.MsgIdHdr)
}

// Entry is the summary of a message in the stream to be cleaned that the cleaner needs.
type Entry struct {
	// EventID is the event id; if empty the message is not deleted.
	EventID string
	Time    time.Time
}

// Log is the face of the stream to be cleaned that the cleaner needs.
type Log interface {
	// Bounds is the first and last sequence number in the stream; last is 0 if the stream is empty.
	Bounds(ctx context.Context) (first, last uint64, err error)
	// Entry is the summary of the message numbered seq; found is false if the message was deleted.
	Entry(ctx context.Context, seq uint64) (e Entry, found bool, err error)
	// Delete deletes the message numbered seq.
	Delete(ctx context.Context, seq uint64) error
}

// Cleaner deletes the old messages of the stream whose receipt is recorded.
type Cleaner struct {
	Log Log
	// Received returns, of the given event ids, those whose receipt is recorded
	// (deliverydb.Store.Received).
	Received func(ctx context.Context, eventIDs []string) (map[string]bool, error)
	// Budget is the largest number of messages examined in a round (default 20000).
	Budget int

	// next is the sequence number the next round starts from: so that old
	// messages left with no receipt are not rescanned every round and use up the budget.
	next uint64
}

const batch = 200

// Clean runs a round: it deletes the messages whose receipt is recorded, not passing
// the sequence number limit (0: no limit) and the first message whose time is not older than
// notBefore; it returns the number of messages deleted. limit is the bound processed
// by the stream's own named consumers (e.g. report and inventory at the store server);
// nothing beyond it is deleted.
func (c *Cleaner) Clean(ctx context.Context, limit uint64, notBefore time.Time) (int, error) {
	first, last, err := c.Log.Bounds(ctx)
	if err != nil || last == 0 {
		return 0, err
	}
	if limit > 0 && limit < last {
		last = limit
	}
	budget := c.Budget
	if budget <= 0 {
		budget = 20000
	}
	type cand struct {
		seq uint64
		id  string
	}
	var pending []cand
	deleted := 0
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		ids := make([]string, len(pending))
		for i, p := range pending {
			ids[i] = p.id
		}
		got, err := c.Received(ctx, ids)
		if err != nil {
			return err
		}
		for _, p := range pending {
			if !got[p.id] {
				continue
			}
			if err := c.Log.Delete(ctx, p.seq); err != nil {
				return err
			}
			deleted++
		}
		pending = pending[:0]
		return nil
	}
	seq := max(c.next, first)
	reachedRecent := false
	for ; seq <= last && budget > 0; seq++ {
		e, found, err := c.Log.Entry(ctx, seq)
		if err != nil {
			return deleted, err
		}
		if !found {
			continue
		}
		budget--
		if !e.Time.Before(notBefore) {
			reachedRecent = true
			break
		}
		if e.EventID == "" {
			continue
		}
		pending = append(pending, cand{seq, e.EventID})
		if len(pending) >= batch {
			if err := flush(); err != nil {
				return deleted, err
			}
		}
	}
	if err := flush(); err != nil {
		return deleted, err
	}
	if reachedRecent || seq > last {
		// No old messages left to scan: the next round starts from the beginning, so old messages
		// whose receipt comes later are deleted too.
		c.next = 0
	} else {
		c.next = seq
	}
	return deleted, nil
}
