// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package cashierproto turns the cashier's latest state into protobuf messages
// (staff.v1.CashierState): the store setting that goes down to the registers (with the PIN
// digest) and the event reported to headquarters (without the PIN digest).
package cashierproto

import (
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/personnel"
)

const (
	// ConfigPrefix is the subject of the cashier that goes down to the registers:
	// config.cashier.<cashier> (the store settings stream; the latest state per cashier).
	ConfigPrefix = "config.cashier."
	// ConfigType is the CloudEvents type of the store setting.
	ConfigType = "com.abarow.config.cashier.v1"
	// EventSubject is the subject of the change reported to headquarters (the store
	// stream, and from there headquarters).
	EventSubject = "store.cashier.changed.v1"
	// EventType is the CloudEvents type of the event.
	EventType = "com.abarow.store.cashier.changed.v1"
)

// Encoder implements personnel.Encoder.
type Encoder struct {
	StoreID string
	// NewID produces a unique event id.
	NewID func() string
}

var _ personnel.Encoder = Encoder{}

// Encode turns the person into two messages: to the registers (config) and to headquarters (event).
// The PIN digest goes only to the registers: headquarters has no business with the PIN.
func (e Encoder) Encode(u personnel.User) ([]outbox.Message, error) {
	pc := personnel.Cashier{StoreID: e.StoreID, ID: u.ID, Name: u.Name, Role: u.Role, PINHash: u.PINHash, Disabled: u.Disabled,
		UpdatedBy: u.UpdatedBy, UpdatedAt: u.UpdatedAt}
	configID, eventID := e.NewID(), e.NewID()
	configData, err := personnel.Encode(pc, configID, true)
	if err != nil {
		return nil, err
	}
	eventData, err := personnel.Encode(pc, eventID, false)
	if err != nil {
		return nil, err
	}
	return []outbox.Message{
		{ID: configID, Type: ConfigType, Subject: ConfigPrefix + u.ID, StoreID: e.StoreID,
			Payload: configData, ContentType: "application/protobuf", OccurredAt: u.UpdatedAt},
		{ID: eventID, Type: EventType, Subject: EventSubject, StoreID: e.StoreID,
			Payload: eventData, ContentType: "application/protobuf", OccurredAt: u.UpdatedAt},
	}, nil
}
