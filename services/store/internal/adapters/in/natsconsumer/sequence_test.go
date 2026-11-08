// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/sequence"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

type fakeMsg struct {
	subject string
	header  nats.Header
	data    []byte
}

func (m fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{Timestamp: time.Now()}, nil
}
func (m fakeMsg) Data() []byte                    { return m.data }
func (m fakeMsg) Headers() nats.Header            { return m.header }
func (m fakeMsg) Subject() string                 { return m.subject }
func (m fakeMsg) Reply() string                   { return "" }
func (m fakeMsg) Ack() error                      { return nil }
func (m fakeMsg) DoubleAck(context.Context) error { return nil }
func (m fakeMsg) Nak() error                      { return nil }
func (m fakeMsg) NakWithDelay(time.Duration) error {
	return nil
}
func (m fakeMsg) InProgress() error           { return nil }
func (m fakeMsg) Term() error                 { return nil }
func (m fakeMsg) TermWithReason(string) error { return nil }

// calls records the consumer's side effects in order.
type calls struct{ log []string }

type reports struct {
	c       *calls
	voidErr error
}

func (r *reports) SaveSale(context.Context, domain.Sale) error { return nil }
func (r *reports) SaveVoid(context.Context, domain.Void) error {
	if r.voidErr != nil {
		return r.voidErr
	}
	r.c.log = append(r.c.log, "void")
	return nil
}
func (r *reports) SaveUnfinished(context.Context, ledger.Unfinished) error { return nil }
func (r *reports) SaveShift(context.Context, domain.Shift) error           { return nil }
func (r *reports) Report(context.Context, ledger.Filter) (ledger.Report, error) {
	return ledger.Report{}, nil
}
func (r *reports) Activity(context.Context) (map[string]domain.Activity, error) { return nil, nil }

type noStream struct{}

func (noStream) Ensure(context.Context, []string) error { return nil }
func (noStream) Sources(context.Context) (map[string]domain.Source, error) {
	return nil, nil
}

type seqs struct {
	c   *calls
	err error
}

func (s *seqs) RecordSequence(_ context.Context, store, register, producer string, seq int64) error {
	if s.err != nil {
		return s.err
	}
	s.c.log = append(s.c.log, "seq:"+store+"/"+register+"/"+producer+"/"+string(rune('0'+seq)))
	return nil
}

type issuer struct{ c *calls }

func (i issuer) IssueReceipt(_ context.Context, owner, stream, id string) error {
	i.c.log = append(i.c.log, "makbuz:"+owner+"/"+stream+"/"+id)
	return nil
}

func voided(t *testing.T, saleID, seq string) fakeMsg {
	t.Helper()
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	data, err := proto.Marshal(&salesv1.SaleVoided{EventId: "e1", StoreId: "store-1", RegisterId: "register-1", OccurredAt: timestamppb.New(at),
		SaleId: saleID, TotalKurus: 999, CashierId: "mert"})
	if err != nil {
		t.Fatal(err)
	}
	h := nats.Header{}
	h.Set("ce-id", "e1")
	h.Set("ce-source", "abarow/store-1/register-1")
	h.Set("ce-time", at.Format(time.RFC3339Nano))
	h.Set("content-type", "application/protobuf")
	h.Set("Nats-Msg-Id", "e1")
	// Origin stream (delivery receipt): the name of the register stream that is a source of the store stream.
	h.Set("Nats-Stream-Source", "ABAROW_SALES")
	if seq != "" {
		h.Set(sequence.HeaderSequence, seq)
		h.Set(sequence.HeaderProducer, "sales")
	}
	return fakeMsg{subject: "sale.voided.v1", header: h, data: data}
}

func handle(t *testing.T, rep *reports, sq Sequencer, iss Issuer, msg fakeMsg) error {
	t.Helper()
	svc := app.NewService(noStream{}, rep, nil, time.UTC)
	var opts []Option
	if sq != nil {
		opts = append(opts, WithSequences(sq))
	}
	c := New(nil, svc, slog.New(slog.NewTextHandler(io.Discard, nil)), iss, opts...)
	return c.Handle(context.Background(), msg)
}

// After the event is processed the sequence number is recorded; the receipt is given after this.
func TestSequenceRecordedBeforeReceipt(t *testing.T) {
	c := &calls{}
	if err := handle(t, &reports{c: c}, &seqs{c: c}, issuer{c}, voided(t, "s1", "7")); err != nil {
		t.Fatal(err)
	}
	if len(c.log) < 2 || c.log[0] != "void" || c.log[1] != "seq:store-1/register-1/sales/"+string(rune('0'+7)) {
		t.Errorf("order: %v", c.log)
	}
	for _, l := range c.log[2:] {
		if len(l) < 7 || l[:7] != "makbuz:" {
			t.Errorf("beklenmeyen yan etki %q (%v)", l, c.log)
		}
	}
}

// An event with no number is skipped; it works even if no Sequencer is given.
func TestSequenceSkippedWithoutHeaders(t *testing.T) {
	c := &calls{}
	if err := handle(t, &reports{c: c}, &seqs{c: c}, nil, voided(t, "s1", "")); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(c.log, func(l string) bool { return len(l) > 4 && l[:4] == "seq:" }) {
		t.Errorf("an event with no headers was recorded: %v", c.log)
	}
	c = &calls{}
	if err := handle(t, &reports{c: c}, nil, nil, voided(t, "s2", "5")); err != nil {
		t.Fatal(err)
	}
}

// If the event could not be processed its number is not recorded; the event comes again.
func TestSequenceNotRecordedWhenSaveFails(t *testing.T) {
	c := &calls{}
	err := handle(t, &reports{c: c, voidErr: errors.New("veritabanı kapalı")}, &seqs{c: c}, issuer{c}, voided(t, "s1", "7"))
	if err == nil || natsio.IsReject(err) {
		t.Fatalf("a retry error was expected: %v", err)
	}
	if len(c.log) != 0 {
		t.Errorf("side effect of the unprocessed event: %v", c.log)
	}
}

// A rejected (incomplete) event has come too: its number is recorded.
func TestSequenceRecordedForRejectedEvent(t *testing.T) {
	c := &calls{}
	err := handle(t, &reports{c: c}, &seqs{c: c}, issuer{c}, voided(t, "", "9"))
	if err == nil || !natsio.IsReject(err) {
		t.Fatalf("an incomplete event should be rejected: %v", err)
	}
	if len(c.log) != 1 || c.log[0] != "seq:store-1/register-1/sales/"+string(rune('0'+9)) {
		t.Errorf("number of the rejected event: %v", c.log)
	}
}

// If the number cannot be recorded the event is not acknowledged and no receipt is given.
func TestSequenceFailureRetriesAndHoldsReceipt(t *testing.T) {
	c := &calls{}
	err := handle(t, &reports{c: c}, &seqs{c: c, err: errors.New("sıra tablosu yazılamadı")}, issuer{c}, voided(t, "s1", "7"))
	if err == nil || natsio.IsReject(err) {
		t.Fatalf("a record error should be a retry error: %v", err)
	}
	for _, l := range c.log {
		if len(l) >= 7 && l[:7] == "makbuz:" {
			t.Errorf("a receipt was given to an event whose number was not recorded: %v", c.log)
		}
	}
}
