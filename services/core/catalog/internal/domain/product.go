// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the catalog concepts.
package domain

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/text"
)

// Unit is the product's sales unit.
type Unit string

const (
	Piece Unit = "piece"
	Kg    Unit = "kg"
)

var ErrInvalidProduct = errors.New("geçersiz ürün")

// Product is a catalog product. The price includes VAT; for weighed products
// it is the price per kilogram.
type Product struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Unit Unit   `json:"unit"`
	// UnitPrice is the price in effect at the query moment (computed from Prices).
	UnitPrice money.Money `json:"unit_price_kurus"`
	TaxRate   money.Rate  `json:"tax_rate_bp"`
	Barcodes  []string    `json:"barcodes"`
	QuickKey  bool        `json:"quick_key"`
	// Prices is the price schedule (headquarters and store prices, including the future-dated
	// ones); see PriceAt.
	Prices []Price `json:"-"`
	// Discontinued: headquarters withdrew the product from sale; it is not found at the register.
	Discontinued bool `json:"-"`

	// Restaurant menu (see pkg/menu): the category (not on the menu if empty), the order
	// on the menu, the kitchen station and the option groups.
	Category  string       `json:"category,omitempty"`
	MenuOrder int          `json:"menu_order,omitempty"`
	Station   string       `json:"station,omitempty"`
	Options   []menu.Group `json:"option_groups,omitempty"`
	// The digest and type of the current image; empty if there is no image.
	ImageSHA256 string `json:"-"`
	ImageType   string `json:"-"`
	// ImageData is the image coming from the initial file (only when loading).
	ImageData []byte `json:"-"`
}

// Image is the product's image.
type Image struct {
	ProductID   string
	SHA256      string
	ContentType string
	Data        []byte
}

// MaxCategoryRunes is the largest number of characters in a category name.
const MaxCategoryRunes = 40

// Origin is where the product came to the register from.
type Origin string

const (
	// OriginSeed: from the initial file (catalog.json).
	OriginSeed Origin = "seed"
	// OriginHeadquarters: from headquarters' catalog stream. A product that came from
	// headquarters is not changed by the initial file.
	OriginHeadquarters Origin = "headquarters"
)

// Validate checks that the product is fit to be recorded.
func (p Product) Validate() error {
	switch {
	case p.ID == "":
		return fmt.Errorf("%w: kimlik boş", ErrInvalidProduct)
	case p.Name == "":
		return fmt.Errorf("%w (%s): ad boş", ErrInvalidProduct, p.ID)
	case p.Unit != Piece && p.Unit != Kg:
		return fmt.Errorf("%w (%s): bilinmeyen birim %q", ErrInvalidProduct, p.ID, p.Unit)
	case p.UnitPrice < 0:
		return fmt.Errorf("%w (%s): fiyat negatif", ErrInvalidProduct, p.ID)
	case p.TaxRate < 0:
		return fmt.Errorf("%w (%s): KDV oranı negatif", ErrInvalidProduct, p.ID)
	}
	if err := ValidatePrices(p.Prices); err != nil {
		return fmt.Errorf("%s: %w", p.ID, err)
	}
	if len([]rune(p.Category)) > MaxCategoryRunes {
		return fmt.Errorf("%w (%s): kategori en çok %d karakter", ErrInvalidProduct, p.ID, MaxCategoryRunes)
	}
	if !menu.ValidStation(p.Station) {
		return fmt.Errorf("%w (%s): bilinmeyen istasyon %q", ErrInvalidProduct, p.ID, p.Station)
	}
	if err := menu.ValidateGroups(p.Options); err != nil {
		return fmt.Errorf("%w (%s): %w", ErrInvalidProduct, p.ID, err)
	}
	return nil
}

// SortMenu orders the menu: the categories by their lowest-ordered product (by
// name if equal), within a category the products by their order and name.
func SortMenu(products []Product) {
	first := map[string]int{}
	for _, p := range products {
		if o, ok := first[p.Category]; !ok || p.MenuOrder < o {
			first[p.Category] = p.MenuOrder
		}
	}
	slices.SortStableFunc(products, func(a, b Product) int {
		if a.Category != b.Category {
			if c := cmp.Compare(first[a.Category], first[b.Category]); c != 0 {
				return c
			}
			return strings.Compare(text.SortKey(a.Category), text.SortKey(b.Category))
		}
		if c := cmp.Compare(a.MenuOrder, b.MenuOrder); c != 0 {
			return c
		}
		return strings.Compare(text.SortKey(a.Name), text.SortKey(b.Name))
	})
}
