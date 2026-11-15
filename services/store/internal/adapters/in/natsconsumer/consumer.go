// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsconsumer is the driving adapter that reads the registers' sale events from the store
// stream (ABAROW_STORE) and processes them into the report database.
package natsconsumer

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/ledger"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/sequence"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

const (
	// Stream is the store server's store stream.
	Stream = "ABAROW_STORE"
	// Durable is the name of report's durable consumer; when report restarts it
	// continues where it left off.
	Durable = "report"
)

// Issuer gives the receipt of a processed event (pkg/delivery): so that after the event is
// processed into the database the register that sent it does not delete the old event without seeing the
// receipt. postgres.Store implements it.
type Issuer interface {
	IssueReceipt(ctx context.Context, owner, stream, eventID string) error
}

// Sequencer records the sequence number of an incoming event (pkg/sequence): a gap
// among the numbers is an event that never came. postgres.Store implements it.
type Sequencer interface {
	RecordSequence(ctx context.Context, store, register, producer string, seq int64) error
}

// Option gives New an optional dependency.
type Option func(*options)

type options struct{ seqs Sequencer }

// WithSequences has the sequence number of every event recorded. An event with no number
// (from before numbering) is skipped. If recording fails the event comes again.
func WithSequences(s Sequencer) Option { return func(o *options) { o.seqs = s } }

// New returns the consumer that processes the registers' sale and shift events into the report
// (pkg/natsio). If issuer is given the register is given a receipt after every event is
// processed; the message is not acknowledged until the receipt is given too. report sets up the stream; if it
// does not exist yet (no register added) it waits and retries. An event that cannot be decoded or is
// incomplete does not get better by retrying; it is written to the log and permanently rejected. An event that
// cannot be processed (e.g. a database error) comes again later.
func New(js jetstream.JetStream, svc *app.Service, log *slog.Logger, issuer Issuer, opts ...Option) *natsio.Consumer {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	// track records the sequence number of the event; an event that cannot be read or processed has
	// come too, it is not counted as missing.
	track := func(ctx context.Context, msg jetstream.Msg) error {
		if o.seqs == nil {
			return nil
		}
		producer, seq, ok := sequence.FromHeaders(msg.Headers())
		if !ok {
			return nil
		}
		store, register := sourceParts(msg.Headers().Get("ce-source"))
		if register == "" {
			return nil
		}
		return o.seqs.RecordSequence(ctx, store, register, producer, seq)
	}
	return &natsio.Consumer{JS: js, Stream: Stream, Config: jetstream.ConsumerConfig{Durable: Durable, FilterSubjects: ledger.Subjects},
		What: "satış olayları", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			body, err := ledger.Decode(msg.Subject(), msg.Headers().Get("content-type"), msg.Data())
			if err != nil {
				if terr := track(ctx, msg); terr != nil {
					return terr
				}
				return natsio.Reject(err)
			}
			switch b := body.(type) {
			case ledger.Sale:
				err = svc.RecordSale(ctx, b)
			case ledger.Void:
				err = svc.RecordVoid(ctx, b)
			case ledger.Unfinished:
				err = svc.RecordUnfinished(ctx, b)
			case ledger.Shift:
				err = svc.RecordShift(ctx, b)
			}
			if errors.Is(err, domain.ErrInvalidEvent) {
				if terr := track(ctx, msg); terr != nil {
					return terr
				}
				return natsio.Reject(err)
			}
			if err != nil {
				return err
			}
			if err := track(ctx, msg); err != nil {
				return err
			}
			if issuer == nil {
				return nil
			}
			// The event is recorded: a receipt to the register. No receipt is given for an event whose register or
			// origin stream is unknown (it is not deleted at the register, the safe side).
			owner := registerFromSource(msg.Headers().Get("ce-source"))
			stream, ok := delivery.Origin(msg.Headers())
			id := delivery.EventID(msg.Headers())
			if owner == "" || !ok || id == "" {
				log.Warn("the event has no register, origin stream or id; no receipt was given", "subject", msg.Subject(), "register", owner, "id", id)
				return nil
			}
			return issuer.IssueReceipt(ctx, owner, stream, id)
		}}
}

// registerFromSource extracts the register id from ce-source ("abarow/<store>/<register>");
// returns empty if the format does not match.
func registerFromSource(src string) string {
	_, register := sourceParts(src)
	return register
}

// sourceParts extracts the store and register ids from ce-source ("abarow/<store>/<register>");
// returns both empty if the format does not match.
func sourceParts(src string) (store, register string) {
	parts := strings.Split(src, "/")
	if len(parts) != 3 || parts[0] != "abarow" {
		return "", ""
	}
	return parts[1], parts[2]
}
