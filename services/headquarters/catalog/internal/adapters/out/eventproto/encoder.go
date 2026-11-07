// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package eventproto turns the product's latest state and image into the protobuf
// messages that go to the registers (catalog.product.v1, catalog.image.v1).
package eventproto

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/outbox"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

const (
	// SubjectPrefix is the subject of the product messages: catalog.product.<code>.
	SubjectPrefix = "catalog.product."
	// Type is the message's CloudEvents type.
	Type = "com.abarow.catalog.product.v1"
	// StoreID is the message's source (ce-source abarow/headquarters/): headquarters.
	StoreID = "headquarters"
	// ImageSubjectPrefix is the subject of the image messages: catalog.image.<code>.
	ImageSubjectPrefix = "catalog.image."
	// ImageType is the CloudEvents type of the image message.
	ImageType = "com.abarow.catalog.image.v1"
)

// Encoder implements app.Encoder with protobuf.
type Encoder struct {
	// NewID produces a unique event id.
	NewID func() string
}

var _ app.Encoder = Encoder{}

// Encode turns the product's latest state into a message.
func (e Encoder) Encode(p domain.Product, at time.Time) (outbox.Message, error) {
	id := e.NewID()
	unit := catalogv1.Unit_UNIT_PIECE
	if p.Unit == domain.Kg {
		unit = catalogv1.Unit_UNIT_KG
	}
	state := &catalogv1.ProductState{
		EventId:    id,
		OccurredAt: timestamppb.New(at),
		Product: &catalogv1.Product{
			Id: p.Code, Name: p.Name, Unit: unit, TaxRateBp: int64(p.TaxRate), Barcodes: p.Barcodes, QuickKey: p.QuickKey,
			Category: p.Category, MenuOrder: int32(p.MenuOrder), Station: p.Station,
			ImageSha256: p.ImageSHA256, ImageType: p.ImageType,
		},
		Discontinued: p.Discontinued,
		UpdatedBy:    p.UpdatedBy,
	}
	for _, g := range p.Options {
		pg := &catalogv1.OptionGroup{Id: g.ID, Name: g.Name, Min: int32(g.Min), Max: int32(g.Max)}
		for _, o := range g.Options {
			pg.Options = append(pg.Options, &catalogv1.Option{Id: o.ID, Name: o.Name, PriceDeltaKurus: int64(o.PriceDelta)})
		}
		state.Product.OptionGroups = append(state.Product.OptionGroups, pg)
	}
	for _, pr := range p.Prices {
		c := &catalogv1.PriceChange{UnitPriceKurus: int64(pr.Amount)}
		if !pr.From.IsZero() {
			c.From = timestamppb.New(pr.From)
		}
		state.Prices = append(state.Prices, c)
	}
	payload, err := proto.Marshal(state)
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: id, Type: Type, Subject: SubjectPrefix + p.Code, StoreID: StoreID,
		Payload: payload, ContentType: "application/protobuf", OccurredAt: at,
	}, nil
}

// EncodeImage turns the product's image into a message (if Data is empty the image
// has been removed).
func (e Encoder) EncodeImage(img domain.Image, at time.Time) (outbox.Message, error) {
	id := e.NewID()
	payload, err := proto.Marshal(&catalogv1.ProductImage{
		EventId: id, OccurredAt: timestamppb.New(at), ProductId: img.Code,
		Sha256: img.SHA256, ContentType: img.ContentType, Data: img.Data,
	})
	if err != nil {
		return outbox.Message{}, err
	}
	return outbox.Message{
		ID: id, Type: ImageType, Subject: ImageSubjectPrefix + img.Code, StoreID: StoreID,
		Payload: payload, ContentType: "application/protobuf", OccurredAt: at,
	}, nil
}
