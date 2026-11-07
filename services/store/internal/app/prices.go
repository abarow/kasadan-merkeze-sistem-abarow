// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/services/store/internal/domain"
)

// Store price (exception): the store manager gives a product a price different from the
// headquarters price on the store server screen (immediately or in the future) or removes the
// exception. The schedule goes down to the registers (the store settings stream) and the
// change is reported to headquarters (the store stream) - both are written to the outbox in the same
// transaction as the change.

// Catalog is the store's copy of headquarters' catalog.
type Catalog interface {
	// UpsertCatalogProduct writes the product's latest state.
	UpsertCatalogProduct(ctx context.Context, p domain.CatalogProduct) error
	// CatalogProducts returns the products with the query in their name, code or barcode
	// (all if the query is empty).
	CatalogProducts(ctx context.Context, query string, limit int) ([]domain.CatalogProduct, error)
	// CatalogProduct returns the product; domain.ErrProductNotFound if none.
	CatalogProduct(ctx context.Context, id string) (domain.CatalogProduct, error)
}

// PriceChange is the record of a change in a store price.
type PriceChange struct {
	ProductID string
	At        time.Time
	By        string
	Detail    string
}

// StorePrices is where the store price schedules are kept.
type StorePrices interface {
	// StorePrices returns the product's store schedule (empty if none).
	StorePrices(ctx context.Context, productID string) ([]domain.PricePoint, error)
	// AllStorePrices are the schedules of the products that have a store schedule.
	AllStorePrices(ctx context.Context) (map[string][]domain.PricePoint, error)
	// SaveStorePrices writes the product's schedule in the same transaction as the change record
	// and the outbox messages.
	SaveStorePrices(ctx context.Context, productID string, points []domain.PricePoint, change PriceChange, msgs []outbox.Message) error
	// PriceHistory returns the product's store price changes from newest to oldest.
	PriceHistory(ctx context.Context, productID string, limit int) ([]PriceChange, error)
}

// PriceEncoder turns the store schedule into the messages that go to the registers (config)
// and to headquarters (event).
type PriceEncoder interface {
	Encode(p domain.CatalogProduct, points []domain.PricePoint, by string, at time.Time) (config, event outbox.Message, err error)
}

// ProductPrices are a product's headquarters and store prices.
type ProductPrices struct {
	Product domain.CatalogProduct
	// Headquarters is the headquarters price in effect right now.
	Headquarters money.Money
	// Store is the store price in effect right now (none if HasStore is false).
	Store    money.Money
	HasStore bool
	// StorePoints is the store schedule; StoreUpcoming and HeadquartersUpcoming are the
	// future-dated records.
	StorePoints, StoreUpcoming, HeadquartersUpcoming []domain.PricePoint
}

// Prices holds the use cases of the store prices.
type Prices struct {
	catalog Catalog
	store   StorePrices
	enc     PriceEncoder
	loc     *time.Location
	now     func() time.Time
}

// NewPrices returns a service that writes future dates in loc time.
func NewPrices(catalog Catalog, store StorePrices, enc PriceEncoder, loc *time.Location) *Prices {
	return &Prices{catalog: catalog, store: store, enc: enc, loc: loc, now: time.Now}
}

// SetClock changes the service's clock (for tests).
func (s *Prices) SetClock(now func() time.Time) { s.now = now }

// ApplyCatalog writes the product's latest state coming from headquarters.
func (s *Prices) ApplyCatalog(ctx context.Context, p domain.CatalogProduct) error {
	if p.ID == "" || p.Name == "" {
		return fmt.Errorf("%w: kimliği ya da adı olmayan ürün", domain.ErrInvalidEvent)
	}
	return s.catalog.UpsertCatalogProduct(ctx, p)
}

// List returns the products with their headquarters and store prices in Turkish
// alphabet order.
func (s *Prices) List(ctx context.Context, query string) ([]ProductPrices, error) {
	products, err := s.catalog.CatalogProducts(ctx, query, 500)
	if err != nil {
		return nil, err
	}
	all, err := s.store.AllStorePrices(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]ProductPrices, len(products))
	for i, p := range products {
		out[i] = s.prices(p, all[p.ID], now)
	}
	col := collate.New(language.Turkish)
	slices.SortStableFunc(out, func(a, b ProductPrices) int {
		if c := col.CompareString(a.Product.Name, b.Product.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.Product.ID, b.Product.ID)
	})
	return out, nil
}

// Get returns the product with its prices and store price changes.
func (s *Prices) Get(ctx context.Context, id string) (ProductPrices, []PriceChange, error) {
	p, err := s.catalog.CatalogProduct(ctx, id)
	if err != nil {
		return ProductPrices{}, nil, err
	}
	points, err := s.store.StorePrices(ctx, id)
	if err != nil {
		return ProductPrices{}, nil, err
	}
	history, err := s.store.PriceHistory(ctx, id, 100)
	if err != nil {
		return ProductPrices{}, nil, err
	}
	return s.prices(p, points, s.now()), history, nil
}

func (s *Prices) prices(p domain.CatalogProduct, points []domain.PricePoint, now time.Time) ProductPrices {
	pp := ProductPrices{Product: p, StorePoints: points, StoreUpcoming: domain.Upcoming(points, now), HeadquartersUpcoming: domain.Upcoming(p.HeadquartersPrices, now)}
	pp.Headquarters, _ = p.HeadquartersPriceAt(now)
	pp.Store, pp.HasStore = domain.StorePriceAt(points, now)
	return pp
}

// SetPrice gives the product a store price valid from the moment from (immediately
// if zero).
func (s *Prices) SetPrice(ctx context.Context, by, id string, amount money.Money, from time.Time) (ProductPrices, error) {
	return s.change(ctx, by, id, func(points []domain.PricePoint, now time.Time) ([]domain.PricePoint, string, error) {
		points, err := domain.ScheduleStorePrice(points, amount, false, from, now)
		return points, fmt.Sprintf("mağaza fiyatı %s TL%s", amount, s.when(from, now)), err
	})
}

// Clear removes the product's store exception from the moment from (immediately
// if zero); the headquarters price becomes valid.
func (s *Prices) Clear(ctx context.Context, by, id string, from time.Time) (ProductPrices, error) {
	return s.change(ctx, by, id, func(points []domain.PricePoint, now time.Time) ([]domain.PricePoint, string, error) {
		points, err := domain.ScheduleStorePrice(points, 0, true, from, now)
		return points, "mağaza fiyatı kaldırıldı, merkez fiyatı geçerli" + s.when(from, now), err
	})
}

// Cancel withdraws a store price record that has not taken effect yet.
func (s *Prices) Cancel(ctx context.Context, by, id string, from time.Time) (ProductPrices, error) {
	return s.change(ctx, by, id, func(points []domain.PricePoint, now time.Time) ([]domain.PricePoint, string, error) {
		points, err := domain.CancelStorePrice(points, from, now)
		return points, fmt.Sprintf("%s itibarıyla verilen kayıt geri alındı", from.In(s.loc).Format("02.01.2006 15:04")), err
	})
}

func (s *Prices) when(from, now time.Time) string {
	if from.IsZero() || !from.After(now) {
		return " (hemen)"
	}
	return ", " + from.In(s.loc).Format("02.01.2006 15:04") + " itibarıyla"
}

func (s *Prices) change(ctx context.Context, by, id string,
	fn func([]domain.PricePoint, time.Time) ([]domain.PricePoint, string, error)) (ProductPrices, error) {
	p, err := s.catalog.CatalogProduct(ctx, id)
	if err != nil {
		return ProductPrices{}, err
	}
	points, err := s.store.StorePrices(ctx, id)
	if err != nil {
		return ProductPrices{}, err
	}
	now := s.now()
	points, detail, err := fn(points, now)
	if err != nil {
		return ProductPrices{}, err
	}
	config, event, err := s.enc.Encode(p, points, by, now)
	if err != nil {
		return ProductPrices{}, err
	}
	change := PriceChange{ProductID: id, At: now, By: by, Detail: detail}
	if err := s.store.SaveStorePrices(ctx, id, points, change, []outbox.Message{config, event}); err != nil {
		return ProductPrices{}, err
	}
	return s.prices(p, points, now), nil
}
