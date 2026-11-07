// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsconsumer is the driving adapter that reads the events coming from the stores from the
// headquarters stream (ABAROW_HEADQUARTERS) and processes them into the headquarters database.
package natsconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/personnel"
	"abarow.com/pkg/sequence"
	"abarow.com/services/headquarters/ingest/internal/adapters/out/natsstream"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// Durable is the name of ingest's durable consumer; when ingest restarts it
// continues where it left off.
const Durable = "ingest"

// ErrNotProtobuf is an event that is not protobuf. Events have been published as protobuf
// since v0.23; older JSON events are not taken into headquarters.
var ErrNotProtobuf = ledger.ErrNotProtobuf

// Sequencer records the sequence number of an incoming register event (pkg/sequence):
// a gap among the numbers is an event that never reached headquarters. postgres.Store
// implements it.
type Sequencer interface {
	RecordSequence(ctx context.Context, store, register, producer string, seq int64) error
}

// Option gives New an optional dependency.
type Option func(*options)

type options struct{ seqs Sequencer }

// WithSequences has the sequence number of every register event recorded. An event with no
// number (the store's own events, those from before numbering) is skipped.
func WithSequences(s Sequencer) Option { return func(o *options) { o.seqs = s } }

// New returns the consumer that processes the stores' events into the headquarters database
// (pkg/natsio). If the stream does not exist yet (no store added) or NATS is
// unreachable it waits and retries. If the consumer is deleted (ingest replay) it is
// rebuilt and the headquarters stream is read from the start; events that came earlier are
// skipped by event id. There is no filter: unknown event kinds are stored in
// their raw form too.
//
// An event that cannot be decoded or is incomplete does not get better by retrying; it is written to the log and
// permanently rejected. An event that cannot be processed (e.g. the database is down) comes again later.
func New(js jetstream.JetStream, svc *app.Service, log *slog.Logger, opts ...Option) *natsio.Consumer {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	// track records the sequence number of the event; a rejected event has come
	// too, it is not counted as missing.
	track := func(ctx context.Context, msg jetstream.Msg, e domain.Event) error {
		if o.seqs == nil || e.RegisterID == "" {
			return nil
		}
		producer, seq, ok := sequence.FromHeaders(msg.Headers())
		if !ok {
			return nil
		}
		return o.seqs.RecordSequence(ctx, e.StoreID, e.RegisterID, producer, seq)
	}
	return &natsio.Consumer{JS: js, Stream: natsstream.StreamName, Config: jetstream.ConsumerConfig{Durable: Durable},
		What: "store events", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			stored := time.Now()
			if md, err := msg.Metadata(); err == nil {
				stored = md.Timestamp
			}
			in, err := Parse(msg.Subject(), msg.Headers(), msg.Data(), stored)
			if err != nil {
				return natsio.Reject(err)
			}
			if in.ClaimedStore != "" {
				// The register is configured with another store's id: the event is written to the store
				// it came from, the configuration must be fixed.
				log.Warn("the store id in the event differs from the store it came from; written to the store it came from",
					"store", in.Event.StoreID, "olaydaki", in.ClaimedStore, "register", in.Event.RegisterID, "ce-id", in.Event.ID)
			}
			_, err = svc.Record(ctx, in.Event, in.Body)
			if errors.Is(err, domain.ErrInvalidEvent) {
				if terr := track(ctx, msg, in.Event); terr != nil {
					return terr
				}
				return natsio.Reject(err)
			}
			if err != nil {
				return err
			}
			return track(ctx, msg, in.Event)
		}}
}

// Incoming is an event coming from the headquarters stream.
type Incoming struct {
	Event domain.Event
	// Body is the decoded content of the event (domain.Sale, Void, Unfinished or
	// Shift); nil for an unknown event kind.
	Body any
	// ClaimedStore is the store id written inside the event, if it is different from the store
	// it came from.
	ClaimedStore string
}

// Parse turns a message in the headquarters stream into an event. The store id
// comes from the subject (store-1.sale.completed.v1); the event id, register and time
// from the CloudEvents headers. stored is the time the message was written to the stream;
// it is used if the event's time is not in the headers.
func Parse(subject string, h nats.Header, data []byte, stored time.Time) (Incoming, error) {
	storeID, evSubject, ok := natsstream.Route(subject)
	if !ok {
		return Incoming{}, fmt.Errorf("no store in the subject (%q): %w", subject, domain.ErrInvalidEvent)
	}
	e := domain.Event{
		ID: h.Get("ce-id"), Subject: evSubject, StoreID: storeID,
		RegisterID: registerFromSource(h.Get("ce-source")), ContentType: h.Get("content-type"), Payload: data,
		OccurredAt: stored,
	}
	if e.ID == "" {
		e.ID = h.Get(nats.MsgIdHdr)
	}
	if t, err := time.Parse(time.RFC3339Nano, h.Get("ce-time")); err == nil {
		e.OccurredAt = t
	}
	body, err := Decode(evSubject, e.ContentType, data)
	if err != nil {
		return Incoming{}, err
	}
	in := Incoming{Event: e}
	// The event is written to the store it came from; the register id comes from inside the event.
	align := func(store, register *string) {
		if *store != "" && *store != storeID {
			in.ClaimedStore = *store
		}
		*store = storeID
		if *register == "" {
			*register = in.Event.RegisterID
		}
		in.Event.RegisterID = *register
	}
	switch b := body.(type) {
	case domain.Sale:
		align(&b.StoreID, &b.RegisterID)
		in.Body = b
	case domain.Void:
		align(&b.StoreID, &b.RegisterID)
		in.Body = b
	case domain.Unfinished:
		align(&b.StoreID, &b.RegisterID)
		in.Body = b
	case domain.Shift:
		align(&b.StoreID, &b.RegisterID)
		in.Body = b
	case domain.StorePrices:
		// The store server's own event: it has no register.
		if b.StoreID != "" && b.StoreID != storeID {
			in.ClaimedStore = b.StoreID
		}
		b.StoreID = storeID
		in.Body = b
	case domain.Cashier:
		if b.StoreID != "" && b.StoreID != storeID {
			in.ClaimedStore = b.StoreID
		}
		b.StoreID = storeID
		in.Body = b
	}
	return in, nil
}

// registerFromSource extracts the register id from ce-source ("abarow/<store>/<register>").
func registerFromSource(src string) string {
	parts := strings.Split(src, "/")
	if len(parts) != 3 || parts[0] != "abarow" {
		return ""
	}
	return parts[2]
}

// Decode decodes the event's content: the sale and shift events with the sales
// ledger's decoder (pkg/ledger), the store's price and cashier events
// here. It returns nil for an event kind headquarters does not know.
func Decode(subject, contentType string, data []byte) (any, error) {
	switch subject {
	case "store.price.changed.v1", "store.cashier.changed.v1":
	default:
		return ledger.Decode(subject, contentType, data)
	}
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return nil, ErrNotProtobuf
	}
	if subject == "store.price.changed.v1" {
		return decodeStorePrices(data)
	}
	return decodeCashier(data)
}

func decodeStorePrices(data []byte) (domain.StorePrices, error) {
	var e catalogv1.StorePrices
	if err := proto.Unmarshal(data, &e); err != nil {
		return domain.StorePrices{}, fmt.Errorf("could not decode store.price.changed.v1: %w", err)
	}
	s := domain.StorePrices{StoreID: e.GetStoreId(), ProductID: e.GetProductId(), ProductName: e.GetProductName(), UpdatedBy: e.GetUpdatedBy()}
	if e.GetOccurredAt() != nil {
		s.At = e.GetOccurredAt().AsTime()
	}
	for _, p := range e.GetPrices() {
		sp := domain.StorePrice{Amount: money.Money(p.GetUnitPriceKurus()), Clear: p.GetClear()}
		if p.GetFrom() != nil {
			sp.From = p.GetFrom().AsTime()
		}
		s.Prices = append(s.Prices, sp)
	}
	return s, nil
}

func decodeCashier(data []byte) (domain.Cashier, error) {
	c, err := personnel.Decode("application/protobuf", data)
	if err != nil {
		return domain.Cashier{}, fmt.Errorf("could not decode store.cashier.changed.v1: %w", err)
	}
	return domain.Cashier{StoreID: c.StoreID, CashierID: c.ID, Name: c.Name, Role: string(c.Role), Disabled: c.Disabled,
		UpdatedBy: c.UpdatedBy, At: c.UpdatedAt}, nil
}
