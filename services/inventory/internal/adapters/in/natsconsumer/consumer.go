// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsconsumer is the NATS driving adapter of the stock service; everything
// is read with a durable consumer (what came while the service was down is processed once it is up).
//
// Store scope: it deducts from stock the completed sales coming from the registers (the store stream),
// applies headquarters' catalog to the product information, and records the transfers and the
// place list coming down from headquarters (the transfer stream).
//
// Headquarters scope: it reads the stores' stock movements from the headquarters stream and
// the products from headquarters' catalog stream.
package natsconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	inventoryv1 "abarow.com/gen/com/abarow/inventory/v1"

	"abarow.com/pkg/catalog"
	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/app"
)

const (
	// Durable is the name of the inventory's durable consumer.
	Durable = "inventory"
	// HeadquartersStream is the headquarters stream that collects the stores' events (ingest
	// creates it); the store id is prepended to the subject.
	HeadquartersStream = "ABAROW_HEADQUARTERS"
	// CatalogStream is headquarters' catalog (catalog-admin creates it).
	CatalogStream = "ABAROW_CATALOG"
	// MovedSubject is the subject of a stock movement (after the store id).
	MovedSubject = "store.stock.moved.v1"

	// StoreStream is the store stream that collects the registers' sales (report
	// creates it; store scope).
	StoreStream = "ABAROW_STORE"
	// SaleSubject is the subject of a completed sale.
	SaleSubject = "sale.completed.v1"
	// TransferStream is this store's copy of headquarters' transfer stream
	// (inventory creates it; store scope).
	TransferStream = "ABAROW_TRANSFERS"
	// LocationsSubject is the subject of the places a transfer can be sent to.
	LocationsSubject = "transfer.locations.v1"
)

// SentSubjects are the subjects of the transfers sent to here
// (transfer.<receiving place>.<transfer>.sent.v1).
func SentSubjects(here string) string { return "transfer." + here + ".*.sent.v1" }

// errInvalid is an event that cannot be read: it is not retried, it is rejected.
var errInvalid = natsio.Reject(errors.New("the event could not be read"))

// kinds is the counterpart of the movement types in the contract.
var kinds = map[inventoryv1.MovementKind]stock.Kind{
	inventoryv1.MovementKind_MOVEMENT_KIND_SALE:         stock.Sale,
	inventoryv1.MovementKind_MOVEMENT_KIND_RECEIPT:      stock.Receipt,
	inventoryv1.MovementKind_MOVEMENT_KIND_COUNT:        stock.Count,
	inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_OUT: stock.TransferOut,
	inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_IN:  stock.TransferIn,
}

// Consumer reads a stream with a durable consumer; if it breaks it sets it up again
// (pkg/natsio).
type Consumer = natsio.Consumer

// Movements is the consumer that processes the stores' stock movements. The place is the
// store id at the start of the subject (ingest adds each store's events with that store's
// id): a store cannot write stock in the name of another store.
func Movements(js jetstream.JetStream, svc *app.Service, log *slog.Logger) *Consumer {
	return &Consumer{JS: js, Stream: HeadquartersStream, Config: config("*." + MovedSubject), What: "store stock", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			location, m, err := DecodeMoved(msg.Subject(), msg.Headers().Get("content-type"), msg.Data())
			if err != nil {
				return err
			}
			return svc.ApplyStoreMovement(ctx, location, m)
		}}
}

// Sales is the consumer that deducts completed sales from stock.
func Sales(js jetstream.JetStream, svc *app.Service, log *slog.Logger) *Consumer {
	return &Consumer{JS: js, Stream: StoreStream, Config: config(SaleSubject), What: "sales", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			s, err := decodeSale(msg)
			if err != nil {
				return err
			}
			lines := make([]stock.Line, 0, len(s.Lines))
			for _, l := range s.Lines {
				lines = append(lines, stock.Line{ProductID: l.ProductID, Name: l.Name, Qty: l.Qty})
			}
			return svc.ApplySale(ctx, s.ID, s.CashierID, s.PaidAt, lines)
		}}
}

// Catalog is the consumer that applies headquarters' products.
func Catalog(js jetstream.JetStream, svc *app.Service, log *slog.Logger) *Consumer {
	return &Consumer{JS: js, Stream: CatalogStream, Config: config("catalog.product.>"), What: "catalog", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			p, err := decodeProduct(msg.Headers().Get("content-type"), msg.Data())
			if err != nil {
				return err
			}
			return svc.ApplyProduct(ctx, p)
		}}
}

// TransferConfig is the transfer stream at the store: source takes from the transfer stream in the JetStream
// domain (headquarters) only the transfers sent to here and the place list.
// The last state is kept per subject (each transfer has its own subject; only
// the last state of the place list is valid). Transfers that came while the connection
// to headquarters was down wait here.
func TransferConfig(source, here string) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              TransferStream,
		Storage:           jetstream.FileStorage,
		MaxMsgsPerSubject: 1,
		Sources: []*jetstream.StreamSource{{Name: TransferStream, Domain: source,
			SubjectTransforms: []jetstream.SubjectTransformConfig{{Source: SentSubjects(here)}, {Source: LocationsSubject}}}},
	}
}

// Transfers is the consumer that records the transfers and the place list coming down
// from headquarters; source is headquarters' JetStream domain, here is this store.
func Transfers(js jetstream.JetStream, svc *app.Service, source, here string, log *slog.Logger) *Consumer {
	return &Consumer{JS: js, Stream: TransferStream, Config: config(), What: "transferler", Log: log,
		Ensure: func(ctx context.Context) error {
			_, err := js.CreateOrUpdateStream(ctx, TransferConfig(source, here))
			return err
		},
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			if err := protobuf(msg.Headers().Get("content-type")); err != nil {
				return err
			}
			if msg.Subject() == LocationsSubject {
				var e inventoryv1.Locations
				if err := proto.Unmarshal(msg.Data(), &e); err != nil {
					return fmt.Errorf("%w: yerler", errInvalid)
				}
				locs := make([]stock.Location, 0, len(e.GetLocations()))
				for _, l := range e.GetLocations() {
					locs = append(locs, stock.Location{ID: l.GetId(), Name: l.GetName()})
				}
				return svc.ApplyLocations(ctx, locs)
			}
			t, err := DecodeTransfer(msg.Data())
			if err != nil {
				return err
			}
			return svc.ApplyTransferSent(ctx, t)
		}}
}

// DecodeTransfer reads the sent transfer event (TransferSent).
func DecodeTransfer(data []byte) (stock.Transfer, error) {
	var e inventoryv1.TransferSent
	if err := proto.Unmarshal(data, &e); err != nil || e.GetTransferId() == "" || e.GetToLocation() == "" {
		return stock.Transfer{}, fmt.Errorf("%w: transfer", errInvalid)
	}
	t := stock.Transfer{ID: e.GetTransferId(), From: e.GetFromLocation(), To: e.GetToLocation(), By: e.GetBy(), At: e.GetOccurredAt().AsTime()}
	for _, l := range e.GetLines() {
		t.Lines = append(t.Lines, stock.Line{ProductID: l.GetProductId(), Name: l.GetProductName(), Qty: money.Qty(l.GetQtyMilli())})
	}
	return t, nil
}

func protobuf(contentType string) error {
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return fmt.Errorf("%w: content type %q", errInvalid, contentType)
	}
	return nil
}

// decodeSale decodes the sale with the sales ledger's decoder (pkg/ledger).
func decodeSale(msg jetstream.Msg) (ledger.Sale, error) {
	body, err := ledger.Decode(SaleSubject, msg.Headers().Get("content-type"), msg.Data())
	s, ok := body.(ledger.Sale)
	if err != nil || !ok || s.ID == "" {
		return ledger.Sale{}, fmt.Errorf("%w: %s", errInvalid, SaleSubject)
	}
	return s, nil
}

// DecodeMoved decodes the stock movement in the headquarters stream.
func DecodeMoved(subject, contentType string, data []byte) (string, stock.Movement, error) {
	location, rest, ok := strings.Cut(subject, ".")
	if !ok || rest != MovedSubject || location == "" {
		return "", stock.Movement{}, fmt.Errorf("%w: konu %q", errInvalid, subject)
	}
	if err := protobuf(contentType); err != nil {
		return "", stock.Movement{}, err
	}
	var e inventoryv1.StockMoved
	if err := proto.Unmarshal(data, &e); err != nil || e.GetMovementId() == "" {
		return "", stock.Movement{}, fmt.Errorf("%w: %s", errInvalid, MovedSubject)
	}
	if e.GetLocationId() != location {
		return "", stock.Movement{}, fmt.Errorf("%w: the place %q in the event of store %s", errInvalid, location, e.GetLocationId())
	}
	kind, ok := kinds[e.GetKind()]
	if !ok {
		return "", stock.Movement{}, fmt.Errorf("%w: movement kind %v", errInvalid, e.GetKind())
	}
	m := stock.Movement{ID: e.GetMovementId(), Kind: kind, Ref: e.GetRef(), Supplier: e.GetSupplier(), Counterpart: e.GetCounterpart(), By: e.GetBy(), At: e.GetOccurredAt().AsTime()}
	for _, l := range e.GetLines() {
		m.Lines = append(m.Lines, stock.Line{
			ProductID: l.GetProductId(), Name: l.GetProductName(), Qty: money.Qty(l.GetQtyMilli()),
			Counted: money.Qty(l.GetCountedMilli()), System: money.Qty(l.GetSystemMilli()),
		})
	}
	return location, m, nil
}

func decodeProduct(contentType string, data []byte) (stock.Product, error) {
	kp, err := catalog.Decode(contentType, data)
	if err != nil || kp.ID == "" {
		return stock.Product{}, fmt.Errorf("%w: product", errInvalid)
	}
	return stock.Product{ID: kp.ID, Name: kp.Name, Unit: stock.Unit(kp.Unit), Barcodes: kp.Barcodes, Discontinued: kp.Discontinued}, nil
}

// config is the inventory's durable consumer.
func config(filter ...string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{Durable: Durable, FilterSubjects: filter}
}
