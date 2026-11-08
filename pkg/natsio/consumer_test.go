// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// fakeMsg is a message that only records its fate.
type fakeMsg struct {
	jetstream.Msg
	outcome string
	delay   time.Duration
}

func (m *fakeMsg) Subject() string      { return "sale.completed.v1" }
func (m *fakeMsg) Headers() nats.Header { return nats.Header{"ce-id": {"e1"}} }
func (m *fakeMsg) Ack() error           { m.outcome = "ack"; return nil }
func (m *fakeMsg) Term() error          { m.outcome = "term"; return nil }
func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.outcome, m.delay = "nak", d
	return nil
}

func TestProcess(t *testing.T) {
	errDB := errors.New("veritabanı kapalı")
	for _, tt := range []struct {
		err     error
		outcome string
		delay   time.Duration
	}{
		{nil, "ack", 0},
		{Reject(errors.New("protobuf değil")), "term", 0},
		{fmt.Errorf("satış: %w", Reject(errors.New("eksik"))), "term", 0},
		{errDB, "nak", RetryDelay},
		{RetryAfter(errDB, 5*time.Second), "nak", 5 * time.Second},
	} {
		c := &Consumer{What: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Handle: func(context.Context, jetstream.Msg) error { return tt.err }}
		m := &fakeMsg{}
		c.process(context.Background(), m)
		if m.outcome != tt.outcome || m.delay != tt.delay {
			t.Errorf("%v: %s %v; istenen %s %v", tt.err, m.outcome, m.delay, tt.outcome, tt.delay)
		}
	}
	if Reject(nil) != nil {
		t.Error("Reject(nil) returned an error")
	}
	// The rejected error carries its own reason.
	if err := Reject(errDB); !errors.Is(err, errDB) {
		t.Errorf("neden kayboldu: %v", err)
	}
}

func TestIsReject(t *testing.T) {
	base := errors.New("bozuk olay")
	if !IsReject(Reject(base)) || !IsReject(fmt.Errorf("sarılı: %w", Reject(base))) {
		t.Error("an error wrapped with Reject was not recognized")
	}
	if IsReject(base) || IsReject(nil) || IsReject(RetryAfter(base, time.Second)) {
		t.Error("an error not made with Reject counted as rejected")
	}
}

// A batch that is written is acknowledged as a whole; a batch that fails goes to Handle
// one by one, which decides about each message.
func TestProcessBatch(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	var handled int
	c := &Consumer{What: "test", Log: quiet,
		Handle: func(_ context.Context, msg jetstream.Msg) error {
			handled++
			if handled == 2 {
				return Reject(errors.New("incomplete"))
			}
			return nil
		},
		HandleBatch: func(context.Context, []jetstream.Msg) error { return nil }}
	ok := []*fakeMsg{{}, {}, {}}
	c.processBatch(context.Background(), []jetstream.Msg{ok[0], ok[1], ok[2]})
	for i, m := range ok {
		if m.outcome != "ack" {
			t.Errorf("written batch, message %d: %s", i, m.outcome)
		}
	}
	if handled != 0 {
		t.Errorf("Handle ran %d times for a written batch", handled)
	}

	c.HandleBatch = func(context.Context, []jetstream.Msg) error { return errors.New("one is invalid") }
	failed := []*fakeMsg{{}, {}, {}}
	c.processBatch(context.Background(), []jetstream.Msg{failed[0], failed[1], failed[2]})
	if got := []string{failed[0].outcome, failed[1].outcome, failed[2].outcome}; got[0] != "ack" || got[1] != "term" || got[2] != "ack" {
		t.Errorf("failed batch, one by one: %v; ack term ack expected", got)
	}
	// A single message does not go through the batch.
	c.HandleBatch = func(context.Context, []jetstream.Msg) error { t.Error("a batch of one"); return nil }
	single := &fakeMsg{}
	c.processBatch(context.Background(), []jetstream.Msg{single})
	if single.outcome != "ack" {
		t.Errorf("single message: %s", single.outcome)
	}
}

// drain takes what is already waiting as one batch and does not wait for more.
func TestDrain(t *testing.T) {
	var sizes []int
	c := &Consumer{What: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handle:      func(context.Context, jetstream.Msg) error { sizes = append(sizes, 1); return nil },
		HandleBatch: func(_ context.Context, msgs []jetstream.Msg) error { sizes = append(sizes, len(msgs)); return nil }}
	queue, stop, done := make(chan jetstream.Msg, 10), make(chan struct{}), make(chan struct{})
	for range 7 {
		queue <- &fakeMsg{}
	}
	go func() { c.drain(context.Background(), queue, stop, 5); close(done) }()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	<-done
	if fmt.Sprint(sizes) != "[5 2]" {
		t.Errorf("batches %v; [5 2] expected", sizes)
	}
}
