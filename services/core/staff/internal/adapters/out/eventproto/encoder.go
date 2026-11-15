// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package eventproto is the app.ShiftEvents adapter that turns shift events into outbox
// messages with the staff.v1 event messages (proto/com/abarow/staff/v1/events.proto).
package eventproto

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/outbox"
	"abarow.com/services/core/staff/internal/adapters/staffpb"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// ContentType is the content type of a protobuf-encoded event.
const ContentType = "application/protobuf"

// Encoder, app.ShiftEvents'i protobuf ile uygular.
type Encoder struct {
	StoreID, RegisterID string
	// NewID produces an event id (ce-id and event_id).
	NewID func() string
}

var _ app.ShiftEvents = Encoder{}

// Opened is the event of the shift opening (shift.opened.v1).
func (e Encoder) Opened(sh domain.Shift) (outbox.Message, error) {
	id := e.NewID()
	return e.message(id, "shift.opened.v1", sh.OpenedAt, &staffv1.ShiftOpened{
		EventId: id, StoreId: e.StoreID, RegisterId: e.RegisterID, OccurredAt: timestamppb.New(sh.OpenedAt),
		Shift: staffpb.Shift(sh),
	})
}

// Closed is the event of the shift closing with its report (shift.closed.v1).
func (e Encoder) Closed(r domain.ShiftReport) (outbox.Message, error) {
	id := e.NewID()
	return e.message(id, "shift.closed.v1", r.Shift.ClosedAt, &staffv1.ShiftClosed{
		EventId: id, StoreId: e.StoreID, RegisterId: e.RegisterID, OccurredAt: timestamppb.New(r.Shift.ClosedAt),
		Report: staffpb.ShiftReport(r),
	})
}

func (e Encoder) message(id, subject string, at time.Time, m proto.Message) (outbox.Message, error) {
	b, err := proto.Marshal(m)
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: id, Type: "com.abarow." + subject, Subject: subject,
		StoreID: e.StoreID, RegisterID: e.RegisterID,
		Payload: b, ContentType: ContentType, OccurredAt: at,
	}, nil
}
