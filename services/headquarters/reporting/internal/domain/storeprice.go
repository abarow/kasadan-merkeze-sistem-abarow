// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/price"
)

// PricePoint is a record of the price schedule (pkg/price): the VAT-included price valid
// from the moment From (from the beginning if From is zero). Clear applies
// only in the store schedule: from that moment the store exception is lifted.
type PricePoint = price.Point

// StorePriceRow is a store's price schedule for a product and the product's headquarters
// price schedule.
type StorePriceRow struct {
	StoreID, StoreName              string
	ProductID, ProductName          string
	StorePrices, HeadquartersPrices []PricePoint
	UpdatedBy                       string
	UpdatedAt                       time.Time
}

// PriceException is a product priced differently from the headquarters price at a store
// (now or at a future date).
type PriceException struct {
	StoreID, StoreName     string
	ProductID, ProductName string
	// Headquarters is the current headquarters price; Store is the current store price (if HasStore
	// is not set the headquarters price is in effect at the store now).
	Headquarters, Store money.Money
	HasStore            bool
	// Upcoming are the future-dated records of the store schedule.
	Upcoming  []PricePoint
	UpdatedBy string
	UpdatedAt time.Time
}

// Difference is the difference of the store price from the headquarters price (store − headquarters).
func (e PriceException) Difference() money.Money { return e.Store - e.Headquarters }

// At is the row's state at the moment t; false if there is neither a current nor a future exception
// at the store.
func (r StorePriceRow) At(t time.Time) (PriceException, bool) {
	e := PriceException{StoreID: r.StoreID, StoreName: r.StoreName, ProductID: r.ProductID, ProductName: r.ProductName,
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
	e.Headquarters, _ = price.At(r.HeadquartersPrices, t)
	e.Store, e.HasStore = price.At(r.StorePrices, t)
	e.Upcoming = price.Upcoming(r.StorePrices, t)
	return e, e.HasStore || len(e.Upcoming) > 0
}
