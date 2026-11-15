// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natscatalog is the driving adapter that takes headquarters' catalog to the register:
// it sets up the catalog stream (ABAROW_CATALOG) in the register's NATS with the store server's as
// a source, reads the products' latest states with its own durable consumer and
// applies them to the catalog.
package natscatalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/catalog"
	"abarow.com/pkg/money"
	"abarow.com/pkg/natsio"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

const (
	// StreamName is the catalog stream (the same name at headquarters, the store server and the register).
	StreamName = "ABAROW_CATALOG"
	// ConfigStream is the store server's store settings stream; the store prices are
	// on the config.price.<product> subject.
	ConfigStream = "ABAROW_STORE_CONFIG"
	// Durable is catalog's durable consumer; when catalog restarts it continues
	// where it left off.
	Durable = "catalog"
)

// StreamConfig is the register's catalog stream: it is fed from the catalog stream in the
// source JetStream domain (the store id on the store server) and from the store
// prices (the config.price.> subjects of the store settings stream).
// Only the latest state per product is kept.
func StreamConfig(source string) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              StreamName,
		Storage:           jetstream.FileStorage,
		MaxMsgsPerSubject: 1,
		Sources: []*jetstream.StreamSource{
			{Name: StreamName, Domain: source},
			{Name: ConfigStream, Domain: source, FilterSubject: "config.price.>"},
		},
	}
}

// New returns the catalog consumer fed from the source JetStream domain (the store server)
// (pkg/natsio): it sets up the stream and applies the products, images and
// store prices. A product that cannot be decoded or is invalid does not get better by retrying;
// it is written to the log and rejected. A product that cannot be applied (e.g.
// a database error) comes again later.
func New(js jetstream.JetStream, svc *app.Service, source string, log *slog.Logger) *natsio.Consumer {
	return &natsio.Consumer{JS: js, Stream: StreamName, Config: jetstream.ConsumerConfig{Durable: Durable},
		What: "merkezin kataloğu", Log: log,
		Ensure: func(ctx context.Context) error {
			_, err := js.CreateOrUpdateStream(ctx, StreamConfig(source))
			return err
		},
		Handle: func(ctx context.Context, msg jetstream.Msg) error { return handle(ctx, svc, msg) }}
}

func handle(ctx context.Context, svc *app.Service, msg jetstream.Msg) error {
	contentType := msg.Headers().Get("content-type")
	var err error
	if strings.HasPrefix(msg.Subject(), "config.price.") {
		var id string
		var prices []domain.Price
		if id, prices, err = DecodeStorePrices(contentType, msg.Data()); err == nil {
			err = svc.ApplyStorePrices(ctx, id, prices)
		}
		// If the product has not come down yet the store price comes again later.
		if errors.Is(err, app.ErrNotFound) {
			return natsio.RetryAfter(err, 5*time.Second)
		}
	} else if strings.HasPrefix(msg.Subject(), ImageSubjectPrefix) {
		var img domain.Image
		if img, err = DecodeImage(contentType, msg.Data()); err == nil {
			err = svc.ApplyImage(ctx, img)
		}
	} else {
		var p domain.Product
		if p, err = Decode(contentType, msg.Data()); err == nil {
			err = svc.Apply(ctx, p)
		}
	}
	if errors.Is(err, domain.ErrInvalidProduct) || errors.Is(err, errUndecodable) {
		return natsio.Reject(err)
	}
	return err
}

// errUndecodable is a message that cannot be read (the same error as pkg/catalog).
var errUndecodable = catalog.ErrUndecodable

// ImageSubjectPrefix is the subject of the product images (catalog.image.<product>).
const ImageSubjectPrefix = "catalog.image."

// DecodeImage decodes headquarters' image message (catalog.image.v1).
func DecodeImage(contentType string, data []byte) (domain.Image, error) {
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return domain.Image{}, fmt.Errorf("%w: içerik türü %q", errUndecodable, contentType)
	}
	var m catalogv1.ProductImage
	if err := proto.Unmarshal(data, &m); err != nil {
		return domain.Image{}, fmt.Errorf("%w: %w", errUndecodable, err)
	}
	return domain.Image{ProductID: m.GetProductId(), SHA256: m.GetSha256(), ContentType: m.GetContentType(), Data: m.GetData()}, nil
}

// Decode turns headquarters' product message (catalog.product.v1) into a product.
func Decode(contentType string, data []byte) (domain.Product, error) {
	kp, err := catalog.Decode(contentType, data)
	if err != nil {
		return domain.Product{}, err
	}
	p := domain.Product{
		ID: kp.ID, Name: kp.Name, Unit: domain.Unit(kp.Unit), TaxRate: kp.TaxRate, Barcodes: kp.Barcodes, QuickKey: kp.QuickKey,
		Discontinued: kp.Discontinued, Category: kp.Category, MenuOrder: kp.MenuOrder, Station: kp.Station, Options: kp.Options,
		ImageSHA256: kp.ImageSHA256, ImageType: kp.ImageType,
	}
	for _, pr := range kp.Prices {
		p.Prices = append(p.Prices, domain.Price{Source: domain.HeadquartersPrice, Amount: pr.Amount, From: pr.From})
	}
	return p, nil
}

// DecodeStorePrices turns the store server's store price message (config.price.<product>)
// into the product id and the store price schedule.
func DecodeStorePrices(contentType string, data []byte) (string, []domain.Price, error) {
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return "", nil, fmt.Errorf("%w: içerik türü %q", errUndecodable, contentType)
	}
	var sp catalogv1.StorePrices
	if err := proto.Unmarshal(data, &sp); err != nil {
		return "", nil, fmt.Errorf("%w: %w", errUndecodable, err)
	}
	var prices []domain.Price
	for _, pr := range sp.GetPrices() {
		price := domain.Price{Source: domain.StorePrice, Amount: money.Money(pr.GetUnitPriceKurus()), Clear: pr.GetClear()}
		if pr.GetFrom() != nil {
			price.From = pr.GetFrom().AsTime()
		}
		prices = append(prices, price)
	}
	return sp.GetProductId(), prices, nil
}
