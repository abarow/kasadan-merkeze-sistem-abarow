// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// draining is a source that loses one event at each check; calls records the order of
// the checks.
type draining struct {
	name    string
	pending int64
	err     error
	calls   *[]string
}

func (d *draining) PendingEvents(context.Context) (int64, error) {
	*d.calls = append(*d.calls, d.name)
	if d.err != nil {
		return 0, d.err
	}
	n := d.pending
	if d.pending > 0 {
		d.pending--
	}
	return n, nil
}

func TestEventsWaitInOrder(t *testing.T) {
	var calls []string
	outbox := &draining{name: "outbox", pending: 2, calls: &calls}
	consumer := &draining{name: "tüketici", pending: 1, calls: &calls}
	e := app.Events{Sources: []app.EventBacklog{outbox, consumer}, Timeout: time.Second, Interval: time.Millisecond}
	if err := e.WaitProcessed(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The consumer is checked after the outbox is empty.
	want := []string{"outbox", "outbox", "outbox", "tüketici", "tüketici"}
	if len(calls) != len(want) {
		t.Fatalf("denetimler %v, istenen %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("denetimler %v, istenen %v", calls, want)
		}
	}
}

func TestEventsTimeout(t *testing.T) {
	var calls []string
	stuck := &draining{name: "outbox", pending: 1 << 40, calls: &calls}
	e := app.Events{Sources: []app.EventBacklog{stuck}, Timeout: 20 * time.Millisecond, Interval: time.Millisecond}
	var pending domain.EventsPendingError
	if err := e.WaitProcessed(context.Background()); !errors.As(err, &pending) || pending.Pending == 0 {
		t.Errorf("no pending events error was returned when the time ran out: %v", err)
	}

	down := errors.New("NATS erişilemiyor")
	broken := &draining{name: "tüketici", err: down, calls: &calls}
	e.Sources = []app.EventBacklog{broken}
	if err := e.WaitProcessed(context.Background()); !errors.As(err, &pending) || !errors.Is(err, down) {
		t.Errorf("error is wrong for a source that cannot be counted: %v", err)
	}
}

type chargedCount int64

func (c chargedCount) CountCharged(context.Context) (int64, error) { return int64(c), nil }

func TestEventsRefuseUnfinishedSales(t *testing.T) {
	var calls []string
	src := &draining{name: "outbox", calls: &calls}
	e := app.Events{Charged: chargedCount(2), Sources: []app.EventBacklog{src}, Timeout: time.Second, Interval: time.Millisecond}
	var unfinished domain.UnfinishedSalesError
	if err := e.WaitProcessed(context.Background()); !errors.As(err, &unfinished) || unfinished.Count != 2 {
		t.Errorf("with a sale whose receipt could not be printed: %v", err)
	}
	if len(calls) != 0 {
		t.Error("events were waited for while there was a sale whose receipt could not be printed")
	}
	e.Charged = chargedCount(0)
	if err := e.WaitProcessed(context.Background()); err != nil {
		t.Errorf("without a sale whose receipt could not be printed: %v", err)
	}
}
