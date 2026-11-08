// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
)

// ReceiptStream is the receipts' stream. The receiver sets it up in its own NATS and
// publishes the receipts here with its outbox; the copy of the sender tier takes it as a
// source (ReceiptConfig).
const ReceiptStream = "ABAROW_RECEIPT"

// ReceiptMaxAge is by default the longest the receipts stay in the stream
// (set with ABAROW_RECEIPT_MAX_AGE). If the sender stays down longer than this it cannot get
// that receipt and its event is not deleted (the safe side).
const ReceiptMaxAge = 30 * 24 * time.Hour

// ReceiptConfig is the receipt stream's setting. The tier writes the receipts it is the
// receiver of to the "receipt.>" subjects; for the copy of the receipts the tier above gave
// it, source (the JetStream domain of the tier above that gives the receipts) and owner (this
// tier's id) are given: only the "receipt.<owner>.>" subjects are taken. If source is empty
// there is no source. maxAge is how long the receipt stays in the stream (0: ReceiptMaxAge).
func ReceiptConfig(source, owner string, maxAge time.Duration) jetstream.StreamConfig {
	if maxAge <= 0 {
		maxAge = ReceiptMaxAge
	}
	cfg := jetstream.StreamConfig{
		Name:     ReceiptStream,
		Subjects: []string{"receipt.>"},
		Storage:  jetstream.FileStorage,
		MaxAge:   maxAge,
		// The outbox may publish the same receipt again; it is de-duplicated by the receipt id.
		Duplicates: 10 * time.Minute,
	}
	if source != "" {
		cfg.Sources = []*jetstream.StreamSource{{
			Name:          ReceiptStream,
			Domain:        source,
			FilterSubject: "receipt." + owner + ".>",
		}}
	}
	return cfg
}

// EnsureStream sets up or updates the stream; it tries until NATS is ready.
// It returns when ctx ends.
func EnsureStream(ctx context.Context, js jetstream.JetStream, cfg jetstream.StreamConfig, log *slog.Logger) {
	cfg, err := natsio.Replicated(cfg)
	for ctx.Err() == nil {
		if err != nil {
			log.Error("could not set up the stream", "stream", cfg.Name, "err", err)
		} else if _, err := js.CreateOrUpdateStream(ctx, cfg); err == nil {
			return
		} else {
			log.Warn("could not set up the stream; will retry", "stream", cfg.Name, "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// JSLog connects a JetStream stream to the cleaner (Log).
type JSLog struct{ S jetstream.Stream }

var _ Log = JSLog{}

// Bounds is the stream's first and last sequence number; last is 0 if empty.
func (j JSLog) Bounds(ctx context.Context) (uint64, uint64, error) {
	info, err := j.S.Info(ctx)
	if err != nil {
		return 0, 0, err
	}
	if info.State.Msgs == 0 {
		return 0, 0, nil
	}
	return info.State.FirstSeq, info.State.LastSeq, nil
}

// Entry is the summary of the message numbered seq; found is false if the message was deleted.
func (j JSLog) Entry(ctx context.Context, seq uint64) (Entry, bool, error) {
	m, err := j.S.GetMsg(ctx, seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return Entry{EventID: EventID(m.Header), Time: m.Time}, true, nil
}

// Delete deletes the message numbered seq.
func (j JSLog) Delete(ctx context.Context, seq uint64) error {
	return j.S.DeleteMsg(ctx, seq)
}

// LocalFloor is the smallest of the acknowledgement floors of the stream's named (durable)
// consumers: that floor and everything before it has been processed by all the local consumers.
// If there is no named consumer ok is false (no limit). The temporary consumers of source
// streams do not count. A consumer that has processed nothing yet (floor 0) stops
// all deletions.
func LocalFloor(ctx context.Context, s jetstream.Stream) (floor uint64, ok bool, err error) {
	it := s.ListConsumers(ctx)
	for ci := range it.Info() {
		if ci.Config.Durable == "" {
			continue
		}
		f := ci.AckFloor.Stream
		if !ok || f < floor {
			floor = f
		}
		ok = true
	}
	return floor, ok, it.Err()
}

// RunCleaner deletes, at cfg.Interval intervals, the messages of the stream whose receipt is recorded and
// that are at least cfg.MaxAge old; it returns when ctx ends. received is the query that gives
// the event ids whose receipt is recorded (deliverydb.Store.Received). A message the stream's own
// named consumers have not processed yet is not deleted. An error is transient (NATS is
// down, the database is down): it is written to the log once and retried in the next round.
func RunCleaner(ctx context.Context, js jetstream.JetStream, stream string, received func(ctx context.Context, eventIDs []string) (map[string]bool, error), cfg CleanConfig, log *slog.Logger) {
	var c *Cleaner
	failing := false
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		n, err := func() (int, error) {
			s, err := js.Stream(ctx, stream)
			if errors.Is(err, jetstream.ErrStreamNotFound) {
				return 0, nil
			}
			if err != nil {
				return 0, err
			}
			if c == nil {
				c = &Cleaner{Log: JSLog{s}, Received: received, Budget: cfg.Budget}
			}
			c.Log = JSLog{s}
			limit, has, err := LocalFloor(ctx, s)
			if err != nil {
				return 0, err
			}
			if has && limit == 0 {
				// A named consumer has not processed anything yet: nothing is deleted.
				return 0, nil
			}
			return c.Clean(ctx, limit, time.Now().Add(-cfg.MaxAge))
		}()
		switch {
		case err != nil:
			if !failing {
				log.Warn(stream+" cleanup failed; will retry", "err", err)
			}
			failing = true
		default:
			failing = false
			if n > 0 {
				log.Info("old events with a recorded receipt were deleted", "stream", stream, "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// NewReceiptConsumer returns the sender's consumer that reads the receipts:
// it gives the receipts on the filter subject of the receipt stream (the copy) in order to
// save; save writes the receipt to the sender's database (deliverydb.Store.SaveReceived)
// and the message is acknowledged only if it returns successfully. A receipt that cannot be read is written to the log
// and permanently rejected. If the stream does not exist yet it waits and retries.
func NewReceiptConsumer(js jetstream.JetStream, durable, filter string, save func(ctx context.Context, r Receipt) error, log *slog.Logger) *natsio.Consumer {
	return &natsio.Consumer{
		JS: js, Stream: ReceiptStream, What: "makbuzlar", Log: log,
		Config: jetstream.ConsumerConfig{Durable: durable, FilterSubject: filter},
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			r, err := ParseReceipt(msg.Data())
			if err != nil {
				return natsio.Reject(fmt.Errorf("%s: %w", msg.Subject(), err))
			}
			return save(ctx, r)
		},
	}
}
