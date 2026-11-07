// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package eventproto encodes the stock service's events (protobuf): the stock movement going
// from the store to headquarters (store.stock.moved.v1) and the transfer and place list going from
// headquarters to the stores.
package eventproto

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "abarow.com/gen/com/abarow/inventory/v1"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/app"
)

const (
	// MovedSubject and MovedType are the subject and type of the stock movement event:
	// it is written to the store stream and goes from there to headquarters.
	MovedSubject = "store.stock.moved.v1"
	MovedType    = "com.abarow.store.stock.moved.v1"
	// SentType is the CloudEvents type of a sent transfer.
	SentType = "com.abarow.transfer.sent.v1"
	// LocationsSubject is the subject of the place list; only the last state
	// is kept.
	LocationsSubject = "transfer.locations.v1"
	// LocationsType, yer listesinin CloudEvents tipidir.
	LocationsType = "com.abarow.transfer.locations.v1"
)

// SentSubject is the subject of the transfer: transfer.<receiving place>.<transfer>.sent.v1.
// Each transfer has its own subject; the receiving store takes only its own
// subjects as its source.
func SentSubject(t stock.Transfer) string { return "transfer." + t.To + "." + t.ID + ".sent.v1" }

// Encoder implements app.Encoder.
type Encoder struct {
	// StoreID is the store of the movement event (store scope).
	StoreID string
	// NewID produces a unique event id.
	NewID func() string
	// Now is the time of the place list (headquarters scope).
	Now func() time.Time
}

var _ app.Encoder = Encoder{}

func (e Encoder) message(subject, typ string, at time.Time, id string, ev proto.Message) (outbox.Message, error) {
	data, err := proto.Marshal(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: id, Type: typ, Subject: subject, StoreID: app.Warehouse,
		Payload: data, ContentType: "application/protobuf", OccurredAt: at,
	}, nil
}

func (e Encoder) Transfer(t stock.Transfer) (outbox.Message, error) {
	id := e.NewID()
	ev := &inventoryv1.TransferSent{
		EventId: id, OccurredAt: timestamppb.New(t.At), TransferId: t.ID,
		FromLocation: t.From, ToLocation: t.To, By: t.By,
	}
	for _, l := range t.Lines {
		ev.Lines = append(ev.Lines, &inventoryv1.MovementLine{ProductId: l.ProductID, ProductName: l.Name, QtyMilli: int64(l.Qty)})
	}
	return e.message(SentSubject(t), SentType, t.At, id, ev)
}

func (e Encoder) Locations(locs []stock.Location) (outbox.Message, error) {
	id, at := e.NewID(), e.Now()
	ev := &inventoryv1.Locations{EventId: id, OccurredAt: timestamppb.New(at)}
	for _, l := range locs {
		ev.Locations = append(ev.Locations, &inventoryv1.Location{Id: l.ID, Name: l.Name})
	}
	return e.message(LocationsSubject, LocationsType, at, id, ev)
}

// Kinds is the counterpart of the movement types in the contract.
var Kinds = map[stock.Kind]inventoryv1.MovementKind{
	stock.Sale:        inventoryv1.MovementKind_MOVEMENT_KIND_SALE,
	stock.Receipt:     inventoryv1.MovementKind_MOVEMENT_KIND_RECEIPT,
	stock.Count:       inventoryv1.MovementKind_MOVEMENT_KIND_COUNT,
	stock.TransferOut: inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_OUT,
	stock.TransferIn:  inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_IN,
}

// Moved turns the movement into the event that will go to headquarters.
func (e Encoder) Moved(m stock.Movement) (outbox.Message, error) {
	id := e.NewID()
	ev := &inventoryv1.StockMoved{
		EventId: id, OccurredAt: timestamppb.New(m.At), LocationId: e.StoreID, MovementId: m.ID,
		Kind: Kinds[m.Kind], Ref: m.Ref, Supplier: m.Supplier, By: m.By, Counterpart: m.Counterpart,
	}
	for _, l := range m.Lines {
		ev.Lines = append(ev.Lines, &inventoryv1.MovementLine{
			ProductId: l.ProductID, ProductName: l.Name, QtyMilli: int64(l.Qty),
			CountedMilli: int64(l.Counted), SystemMilli: int64(l.System),
		})
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: id, Type: MovedType, Subject: MovedSubject, StoreID: e.StoreID,
		Payload: data, ContentType: "application/protobuf", OccurredAt: m.At,
	}, nil
}
