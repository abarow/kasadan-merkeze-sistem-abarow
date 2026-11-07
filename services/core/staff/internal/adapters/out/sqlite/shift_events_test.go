// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/services/core/staff/internal/adapters/out/eventproto"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// TestShiftEvents: opening and closing a shift write an event to the outbox in the same
// transaction as the record; a rejected operation writes no event.
func TestShiftEvents(t *testing.T) {
	ctx := context.Background()
	_, st, now := newAuth(t)
	shifts := app.NewShiftService(st, st, app.Events{})
	shifts.Now = func() time.Time { return *now }
	n := 0
	shifts.Announcer = eventproto.Encoder{StoreID: "store-1", RegisterID: "register-2", NewID: func() string { n++; return fmt.Sprintf("ev-%d", n) }}

	sh, err := shifts.Open(ctx, "ayse", 50000)
	if err != nil {
		t.Fatal(err)
	}
	// A second shift is not opened; no event is written either.
	var open domain.ShiftOpenError
	if _, err := shifts.Open(ctx, "ali", 0); !errors.As(err, &open) {
		t.Fatalf("ikinci vardiya: %v", err)
	}
	pending, err := st.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("açılıştan sonra outbox: %d, %v", len(pending), err)
	}
	m := pending[0]
	if m.ID != "ev-1" || m.Subject != "shift.opened.v1" || m.Type != "com.abarow.shift.opened.v1" ||
		m.StoreID != "store-1" || m.RegisterID != "register-2" || m.ContentType != eventproto.ContentType || !m.OccurredAt.Equal(sh.OpenedAt) {
		t.Errorf("açılış mesajı: %+v", m.Message)
	}
	var opened staffv1.ShiftOpened
	if err := proto.Unmarshal(m.Payload, &opened); err != nil {
		t.Fatal(err)
	}
	if opened.GetEventId() != "ev-1" || opened.GetRegisterId() != "register-2" || opened.GetShift().GetId() != sh.ID ||
		opened.GetShift().GetCashierId() != "ayse" || opened.GetShift().GetOpeningCashKurus() != 50000 {
		t.Errorf("açılış olayı: %v", &opened)
	}

	*now = now.Add(8 * time.Hour)
	r, err := shifts.Close(ctx, "ayse", 49900)
	if err != nil {
		t.Fatal(err)
	}
	// No event is written when there is no shift to close.
	if _, err := shifts.Close(ctx, "ayse", 0); !errors.Is(err, domain.ErrNoShift) {
		t.Fatalf("ikinci kapanış: %v", err)
	}
	if pending, err = st.Pending(ctx, 10); err != nil || len(pending) != 2 {
		t.Fatalf("kapanıştan sonra outbox: %d, %v", len(pending), err)
	}
	m = pending[1]
	if m.Subject != "shift.closed.v1" || !m.OccurredAt.Equal(*now) {
		t.Errorf("kapanış mesajı: %+v", m.Message)
	}
	var closed staffv1.ShiftClosed
	if err := proto.Unmarshal(m.Payload, &closed); err != nil {
		t.Fatal(err)
	}
	rep := closed.GetReport()
	if rep.GetShift().GetId() != sh.ID || rep.GetShift().GetClosedBy() != "ayse" || rep.GetShift().GetCountedCashKurus() != 49900 ||
		rep.GetExpectedCashKurus() != int64(r.ExpectedCash) || rep.GetDifferenceKurus() != -100 || rep.GetShift().GetClosedAt() == nil {
		t.Errorf("kapanış olayı: %v", &closed)
	}

	// As the relay publishes, the pending count falls.
	if err := st.MarkPublished(ctx, pending[0].Seq, *now); err != nil {
		t.Fatal(err)
	}
	if c, err := st.CountPending(ctx); err != nil || c != 1 {
		t.Errorf("bekleyen %d, %v", c, err)
	}
}
