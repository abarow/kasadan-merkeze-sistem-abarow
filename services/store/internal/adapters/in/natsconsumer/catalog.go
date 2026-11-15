// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"context"
	"errors"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/catalog"
	"abarow.com/pkg/natsio"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

// CatalogStream is the store's copy of headquarters' catalog.
const CatalogStream = "ABAROW_CATALOG"

// NewCatalog returns the consumer that reads headquarters' catalog for the store prices screen
// (product names, barcodes, headquarters prices; pkg/natsio).
// The images (catalog.image.>) are no concern of the store prices.
func NewCatalog(js jetstream.JetStream, svc *app.Prices, log *slog.Logger) *natsio.Consumer {
	return &natsio.Consumer{JS: js, Stream: CatalogStream,
		Config: jetstream.ConsumerConfig{Durable: Durable, FilterSubject: "catalog.product.>"},
		What:   "merkezin kataloğu", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			p, err := DecodeProduct(msg.Headers().Get("content-type"), msg.Data())
			if err == nil {
				err = svc.ApplyCatalog(ctx, p)
			}
			if errors.Is(err, catalog.ErrUndecodable) {
				return natsio.Reject(err)
			}
			return err
		}}
}

// DecodeProduct decodes headquarters' product message (catalog.product.v1).
func DecodeProduct(contentType string, data []byte) (domain.CatalogProduct, error) {
	kp, err := catalog.Decode(contentType, data)
	if err != nil {
		return domain.CatalogProduct{}, err
	}
	return domain.CatalogProduct{ID: kp.ID, Name: kp.Name, Unit: kp.Unit, Barcodes: kp.Barcodes, HeadquartersPrices: kp.Prices,
		Discontinued: kp.Discontinued}, nil
}
