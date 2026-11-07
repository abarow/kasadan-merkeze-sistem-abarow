// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package catalog is the single type and the single decoder of the product message
// (catalog.product.<code>, catalog.v1.ProductState) in headquarters' catalog stream.
// The register, the store server (price screen and stock) and headquarters' stock
// fill their own product copies with the same decoder; the search key
// is in pkg/text.
package catalog

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/price"
)

// Birimler.
const (
	Piece = "piece"
	Kg    = "kg"
)

// ErrUndecodable is a product message that cannot be read (not protobuf or corrupt);
// it does not get better by retrying.
var ErrUndecodable = errors.New("ürün mesajı çözülemedi")

// Product is the latest state of one of headquarters' products.
type Product struct {
	ID, Name string
	// Unit: Piece or Kg.
	Unit     string
	TaxRate  money.Rate
	Barcodes []string
	QuickKey bool
	// Restaurant: menu category and order, station, option groups.
	Category  string
	MenuOrder int
	Station   string
	Options   []menu.Group
	// ImageSHA256 and ImageType are the digest and type of the current image (the image
	// comes in a separate message).
	ImageSHA256, ImageType string
	// Prices is the headquarters price schedule (sorted by effective moment).
	Prices       []price.Point
	Discontinued bool
	// UpdatedBy is the headquarters user who made the change.
	UpdatedBy string
}

// Decode decodes the product message.
func Decode(contentType string, data []byte) (Product, error) {
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return Product{}, fmt.Errorf("%w: içerik türü %q", ErrUndecodable, contentType)
	}
	var state catalogv1.ProductState
	if err := proto.Unmarshal(data, &state); err != nil {
		return Product{}, fmt.Errorf("%w: %w", ErrUndecodable, err)
	}
	pp := state.GetProduct()
	p := Product{
		ID: pp.GetId(), Name: pp.GetName(), Unit: Piece, TaxRate: money.Rate(pp.GetTaxRateBp()),
		Barcodes: pp.GetBarcodes(), QuickKey: pp.GetQuickKey(), Discontinued: state.GetDiscontinued(),
		Category: pp.GetCategory(), MenuOrder: int(pp.GetMenuOrder()), Station: pp.GetStation(),
		ImageSHA256: pp.GetImageSha256(), ImageType: pp.GetImageType(), UpdatedBy: state.GetUpdatedBy(),
	}
	if pp.GetUnit() == catalogv1.Unit_UNIT_KG {
		p.Unit = Kg
	}
	for _, g := range pp.GetOptionGroups() {
		group := menu.Group{ID: g.GetId(), Name: g.GetName(), Min: int(g.GetMin()), Max: int(g.GetMax())}
		for _, o := range g.GetOptions() {
			group.Options = append(group.Options, menu.Option{ID: o.GetId(), Name: o.GetName(), PriceDelta: money.Money(o.GetPriceDeltaKurus())})
		}
		p.Options = append(p.Options, group)
	}
	for _, pr := range state.GetPrices() {
		pt := price.Point{Amount: money.Money(pr.GetUnitPriceKurus())}
		if pr.GetFrom() != nil {
			pt.From = pr.GetFrom().AsTime()
		}
		p.Prices = append(p.Prices, pt)
	}
	return p, nil
}
