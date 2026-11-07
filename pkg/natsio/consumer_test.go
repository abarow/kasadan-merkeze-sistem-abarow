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
