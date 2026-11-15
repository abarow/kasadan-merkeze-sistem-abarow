// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package priceproto turns the store price schedule into protobuf messages
// (catalog.v1.StorePrices): the store setting that goes down to the registers and the event
// reported to headquarters.
package priceproto

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/outbox"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

const (
	// ConfigPrefix is the subject of the store price that goes down to the registers:
	// config.price.<product> (the store settings stream; the latest state per product).
	ConfigPrefix = "config.price."
	// ConfigType is the CloudEvents type of the store setting.
	ConfigType = "com.abarow.config.price.v1"
	// EventSubject is the subject of the change reported to headquarters (the store
	// stream, and from there headquarters).
	EventSubject = "store.price.changed.v1"
	// EventType is the CloudEvents type of the event.
	EventType = "com.abarow.store.price.changed.v1"
)

// Encoder implements app.PriceEncoder.
type Encoder struct {
	StoreID string
	// NewID produces a unique event id.
	NewID func() string
}

var _ app.PriceEncoder = Encoder{}

// Encode turns the schedule into two messages; the content is the same, the subject and the id differ.
func (e Encoder) Encode(p domain.CatalogProduct, points []domain.PricePoint, by string, at time.Time) (config, event outbox.Message, err error) {
	encode := func(id string) ([]byte, error) {
		m := &catalogv1.StorePrices{
			EventId: id, OccurredAt: timestamppb.New(at), StoreId: e.StoreID, ProductId: p.ID, UpdatedBy: by, ProductName: p.Name,
		}
		for _, pt := range points {
			c := &catalogv1.StorePriceChange{UnitPriceKurus: int64(pt.Amount), Clear: pt.Clear}
			if !pt.From.IsZero() {
				c.From = timestamppb.New(pt.From)
			}
			m.Prices = append(m.Prices, c)
		}
		return proto.Marshal(m)
	}
	configID, eventID := e.NewID(), e.NewID()
	configData, err := encode(configID)
	if err != nil {
		return config, event, err
	}
	eventData, err := encode(eventID)
	if err != nil {
		return config, event, err
	}
	config = outbox.Message{ID: configID, Type: ConfigType, Subject: ConfigPrefix + p.ID, StoreID: e.StoreID,
		Payload: configData, ContentType: "application/protobuf", OccurredAt: at}
	event = outbox.Message{ID: eventID, Type: EventType, Subject: EventSubject, StoreID: e.StoreID,
		Payload: eventData, ContentType: "application/protobuf", OccurredAt: at}
	return config, event, nil
}
