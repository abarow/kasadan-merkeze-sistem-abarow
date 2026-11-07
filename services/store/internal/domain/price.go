// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/price"
)

// CatalogProduct is a product coming to the store from headquarters' catalog (the store's
// copy of the headquarters catalog stream).
type CatalogProduct struct {
	ID, Name string
	// Unit: piece or kg.
	Unit     string
	Barcodes []string
	// MerkezPrices, merkez fiyat takvimidir.
	HeadquartersPrices []PricePoint
	Discontinued       bool
}

// PricePoint is a record of the price schedule (pkg/price): the VAT-included price valid
// from the moment From (from the beginning if From is zero). Clear is used only in the
// store schedule: the store exception is lifted at the moment From and the headquarters price
// becomes valid.
type PricePoint = price.Point

var (
	// ErrInvalidPrice means the price or the schedule is invalid.
	ErrInvalidPrice = price.ErrInvalid
	// ErrProductNotFound means the product is not in headquarters' catalog.
	ErrProductNotFound = errors.New("ürün merkezin kataloğunda yok")
)

// HeadquartersPriceAt is the headquarters price in effect at the moment t.
func (p CatalogProduct) HeadquartersPriceAt(t time.Time) (money.Money, bool) {
	return price.At(p.HeadquartersPrices, t)
}

// StorePriceAt is the store price in effect at the moment t; false if there is no
// exception or it was lifted.
func StorePriceAt(points []PricePoint, t time.Time) (money.Money, bool) { return price.At(points, t) }

// Upcoming are the records that will take effect after t.
func Upcoming(points []PricePoint, t time.Time) []PricePoint { return price.Upcoming(points, t) }

// ScheduleStorePrice adds a price valid from the moment from (from now if zero or
// in the past) to the store schedule. If clear, the exception is lifted from that moment.
// A record given at the same moment is replaced by the new one;
// records left in the past are dropped.
func ScheduleStorePrice(points []PricePoint, amount money.Money, clear bool, from, now time.Time) ([]PricePoint, error) {
	return price.Plan(points, amount, clear, from, now)
}

// CancelStorePrice removes a record that has not taken effect yet from the schedule.
func CancelStorePrice(points []PricePoint, from, now time.Time) ([]PricePoint, error) {
	out, err := price.Cancel(points, from, now)
	switch {
	case errors.Is(err, price.ErrStarted):
		return nil, fmt.Errorf("%w: %w; yeni fiyat verin ya da istisnayı kaldırın", ErrInvalidPrice, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInvalidPrice, err)
	}
	return out, nil
}

// PruneStorePrices drops the records that can no longer take effect and sorts the schedule
// by effective moment: the price in effect now counts as from the beginning
// (a record in effect that is a lifting is dropped), the later ones stay.
func PruneStorePrices(points []PricePoint, now time.Time) []PricePoint {
	return price.Prune(points, now)
}
