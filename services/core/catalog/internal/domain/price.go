// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/price"
)

// PriceSource is who gave the price.
type PriceSource string

const (
	// HeadquartersPrice is the price given by headquarters; it is valid in
	// all the stores.
	HeadquartersPrice PriceSource = "headquarters"
	// StorePrice is the price the store manager gives for their own store
	// (the store exception); it takes precedence over the headquarters price.
	StorePrice PriceSource = "store"
)

// ErrNoPrice means the product has no price in effect at that moment (e.g. a product whose sale
// will start at a future date).
var ErrNoPrice = errors.New("ürünün fiyatı henüz yürürlükte değil")

// Price is a record of the price schedule: it is valid starting from the moment From;
// from the beginning if From is zero. The price includes VAT; for a weighed product it is the
// price per kilogram.
//
// Clear is used only in a store record: the store exception is lifted at the moment From,
// and from that moment the headquarters price is valid.
type Price struct {
	Source PriceSource
	Amount money.Money
	From   time.Time
	Clear  bool
}

// ValidatePrices checks the schedule: the source is known, the amount is not negative,
// there are not two records at the same moment in the same source, a headquarters record cannot be a removal.
func ValidatePrices(prices []Price) error {
	seen := map[PriceSource]map[int64]bool{}
	for _, p := range prices {
		switch {
		case p.Source != HeadquartersPrice && p.Source != StorePrice:
			return fmt.Errorf("%w: bilinmeyen fiyat kaynağı %q", ErrInvalidProduct, p.Source)
		case p.Amount < 0:
			return fmt.Errorf("%w: fiyat negatif", ErrInvalidProduct)
		case p.Clear && p.Source == HeadquartersPrice:
			return fmt.Errorf("%w: merkez fiyatı kaldırılamaz, yeni fiyat verilir", ErrInvalidProduct)
		}
		if seen[p.Source] == nil {
			seen[p.Source] = map[int64]bool{}
		}
		at := p.From.UnixMilli()
		if p.From.IsZero() {
			at = 0
		}
		if seen[p.Source][at] {
			return fmt.Errorf("%w: aynı anda iki %s fiyatı", ErrInvalidProduct, p.Source)
		}
		seen[p.Source][at] = true
	}
	return nil
}

// points is the source's schedule (pkg/price).
func points(prices []Price, source PriceSource) []price.Point {
	var out []price.Point
	for _, p := range prices {
		if p.Source == source {
			out = append(out, price.Point{Amount: p.Amount, From: p.From, Clear: p.Clear})
		}
	}
	return out
}

// current returns the source's last record in effect at the moment t.
func current(prices []Price, source PriceSource, t time.Time) (Price, bool) {
	p, ok := price.Current(points(prices, source), t)
	return Price{Source: source, Amount: p.Amount, From: p.From, Clear: p.Clear}, ok
}

// PriceAt returns the price valid at the moment t and its source: the last store price
// in effect (if not lifted), otherwise the last headquarters price in effect
// (the rule is in pkg/price; the same as the store server's and headquarters' screens). If neither
// exists it returns ErrNoPrice.
func PriceAt(prices []Price, t time.Time) (money.Money, PriceSource, error) {
	amount, store, ok := price.Effective(points(prices, HeadquartersPrice), points(prices, StorePrice), t)
	switch {
	case !ok:
		return 0, "", ErrNoPrice
	case store:
		return amount, StorePrice, nil
	}
	return amount, HeadquartersPrice, nil
}

// PrunePrices drops the records that can never take effect: in each source
// the records after t and the last record in effect at the moment t stay.
// The result is sorted by effective moment.
func PrunePrices(prices []Price, t time.Time) []Price {
	var out []Price
	for _, source := range []PriceSource{HeadquartersPrice, StorePrice} {
		if p, ok := current(prices, source, t); ok {
			out = append(out, p)
		}
		for _, p := range prices {
			if p.Source == source && p.From.After(t) {
				out = append(out, p)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Price) int { return a.From.Compare(b.From) })
	return out
}
