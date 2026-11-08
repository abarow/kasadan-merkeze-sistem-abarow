// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/natsio"
	"abarow.com/pkg/sequence"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// fakeMsg is the message given to the consumer's Handle.
type fakeMsg struct {
	subject string
	header  nats.Header
	data    []byte
}

func (m fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{Timestamp: time.Now()}, nil
}
func (m fakeMsg) Data() []byte                     { return m.data }
func (m fakeMsg) Headers() nats.Header             { return m.header }
func (m fakeMsg) Subject() string                  { return m.subject }
func (m fakeMsg) Reply() string                    { return "" }
func (m fakeMsg) Ack() error                       { return nil }
func (m fakeMsg) DoubleAck(context.Context) error  { return nil }
func (m fakeMsg) Nak() error                       { return nil }
func (m fakeMsg) NakWithDelay(time.Duration) error { return nil }
func (m fakeMsg) InProgress() error                { return nil }
func (m fakeMsg) Term() error                      { return nil }
func (m fakeMsg) TermWithReason(string) error      { return nil }

type seqCall struct {
	store, register, producer string
	seq                       int64
}

type recSeqs struct {
	calls []seqCall
	err   error
}

func (r *recSeqs) RecordSequence(_ context.Context, store, register, producer string, seq int64) error {
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, seqCall{store, register, producer, seq})
	return nil
}

// failStore makes Record always fail.
type failStore struct{ memStore }

func (f *failStore) Record(context.Context, domain.Event, any) (bool, error) {
	return false, errors.New("veritabanı kapalı")
}

func voided(t *testing.T, saleID string, seq string) fakeMsg {
	t.Helper()
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	data, err := proto.Marshal(&salesv1.SaleVoided{EventId: "e-" + saleID, StoreId: "store-1", RegisterId: "register-1", OccurredAt: timestamppb.New(at),
		SaleId: saleID, TotalKurus: 999, CashierId: "mert"})
	if err != nil {
		t.Fatal(err)
	}
	h := headers("e-"+saleID, "abarow/store-1/register-1", at)
	if seq != "" {
		h.Set(sequence.HeaderSequence, seq)
		h.Set(sequence.HeaderProducer, "sales")
	}
	return fakeMsg{subject: "store-1.sale.voided.v1", header: h, data: data}
}

func handle(store app.Store, seqs Sequencer, msg fakeMsg) error {
	svc := app.NewService(noStream{}, store, nil)
	var opts []Option
	if seqs != nil {
		opts = append(opts, WithSequences(seqs))
	}
	c := New(nil, svc, slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
	return c.Handle(context.Background(), msg)
}

// The sequence number of a processed register event is recorded with (store, register, sender).
func TestSequenceRecordedAfterProcessing(t *testing.T) {
	rec := &recSeqs{}
	if err := handle(&memStore{seen: map[string]bool{}}, rec, voided(t, "s1", "7")); err != nil {
		t.Fatal(err)
	}
	want := []seqCall{{"store-1", "register-1", "sales", 7}}
	if len(rec.calls) != 1 || rec.calls[0] != want[0] {
		t.Errorf("records %v, want %v", rec.calls, want)
	}
}

// An event with no number (the store's own events, those from before numbering) is skipped.
func TestSequenceSkippedWithoutHeaders(t *testing.T) {
	rec := &recSeqs{}
	if err := handle(&memStore{seen: map[string]bool{}}, rec, voided(t, "s1", "")); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("an event with no headers was recorded: %v", rec.calls)
	}
	// It works even if no Sequencer is given.
	if err := handle(&memStore{seen: map[string]bool{}}, nil, voided(t, "s2", "3")); err != nil {
		t.Fatal(err)
	}
}

// If the event could not be processed (the database is down) the number is not recorded; the event
// comes again and is recorded then.
func TestSequenceNotRecordedWhenProcessingFails(t *testing.T) {
	rec := &recSeqs{}
	err := handle(&failStore{memStore{seen: map[string]bool{}}}, rec, voided(t, "s1", "7"))
	if err == nil || natsio.IsReject(err) {
		t.Fatalf("an event that could not be processed should be retried: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("the number of an unprocessed event was recorded: %v", rec.calls)
	}
}

// A rejected (incomplete) event has come too: its number is recorded, so it is not counted
// as missing; the problem is written to the log separately.
func TestSequenceRecordedForRejectedEvent(t *testing.T) {
	rec := &recSeqs{}
	err := handle(&memStore{seen: map[string]bool{}}, rec, voided(t, "", "9"))
	if err == nil || !natsio.IsReject(err) {
		t.Fatalf("an incomplete event should be rejected: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].seq != 9 {
		t.Errorf("the number of the rejected event was not recorded: %v", rec.calls)
	}
}

// If the number cannot be recorded the event is not acknowledged, it comes again (it must not stay without a number).
func TestSequenceFailureRetriesEvent(t *testing.T) {
	rec := &recSeqs{err: errors.New("sıra tablosu yazılamadı")}
	err := handle(&memStore{seen: map[string]bool{}}, rec, voided(t, "s1", "7"))
	if err == nil || natsio.IsReject(err) || !strings.Contains(err.Error(), "sıra tablosu") {
		t.Errorf("a record error should be a retry error: %v", err)
	}
}
