// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the use cases and ports of the market module.
package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/modules/retail/internal/domain"
)

var (
	ErrNotFound        = errors.New("sepet bulunamadı")
	ErrProductNotFound = errors.New("ürün bulunamadı")
)

// Baskets keeps the baskets. Baskets are persistent: even if the service or the screen
// restarts the cashier continues from their basket.
type Baskets interface {
	// Get returns a copy of the basket; ErrNotFound if none.
	Get(ctx context.Context, id domain.BasketID) (*domain.Basket, error)
	Save(ctx context.Context, b *domain.Basket) error
	// Latest returns the cashier's most recent basket changed after since;
	// ErrNotFound if none.
	Latest(ctx context.Context, cashierID string, since time.Time) (*domain.Basket, error)
	// DeleteBefore deletes the baskets last changed before the given time.
	DeleteBefore(ctx context.Context, before time.Time) error
}

const (
	// ResumeWindow is how long ago at most the basket the cashier can continue when the
	// screen reopens may have changed (the session duration); an older basket
	// is stale and a new basket is opened.
	ResumeWindow = 12 * time.Hour
	// Retention is how long baskets are kept; the sale is permanent in sales,
	// the basket is only the cashier's working copy.
	Retention = 7 * 24 * time.Hour
)

// Products, catalog servisidir.
type Products interface {
	// ByBarcode returns the product that corresponds to the barcode; ErrProductNotFound if none.
	ByBarcode(ctx context.Context, barcode string) (domain.Product, error)
	// Search returns the products with the query in their name; the quick keys if the query is empty.
	Search(ctx context.Context, query string, limit int) ([]domain.Product, error)
}

// Sales is the sales service in the core.
// Scale is the scale at the register (device). The weight is in grams (a thousandth of
// the quantity for a kilogram product). ErrNoScale if there is no scale; if the weight
// cannot be given (swinging, empty, negative) an error to show the cashier is returned.
type Scale interface {
	Weigh(ctx context.Context) (money.Qty, error)
}

// ErrNoScale means there is no scale at the register.
var ErrNoScale = errors.New("terazi yok")

type Sales interface {
	// CreateSale creates an open sale in the cashier's name from the basket's lines
	// and returns its id.
	CreateSale(ctx context.Context, cashierID string, lines []domain.Line) (string, error)
}

// Service, sepet use case'leridir.
type Service struct {
	baskets  Baskets
	products Products
	sales    Sales
	newID    func() string
	scale    Scale // terazisiz kurulumda nil
	// Now returns the current time; it can be changed in tests.
	Now func() time.Time

	// mu puts the basket changes in a queue. With a fast barcode scanner the
	// scans come at the same time; without a queue both would read the basket as it was
	// and save it and one would be lost (an incomplete amount for the customer). A scan
	// that comes while moving to payment also does not enter a basket whose sale has been created. Since a single
	// cashier works at the register one lock is enough.
	mu sync.Mutex
}

// NewService binds the ports.
func NewService(baskets Baskets, products Products, sales Sales, newID func() string) *Service {
	return &Service{baskets: baskets, products: products, sales: sales, newID: newID, Now: time.Now}
}

// WithScale connects the scale at the register.
func (s *Service) WithScale(sc Scale) *Service {
	s.scale = sc
	return s
}

// NewBasket opens a new and empty basket for the cashier; it also cleans up the baskets
// whose retention period has passed.
func (s *Service) NewBasket(ctx context.Context, cashierID string) (*domain.Basket, error) {
	now := s.Now()
	if err := s.baskets.DeleteBefore(ctx, now.Add(-Retention)); err != nil {
		return nil, err
	}
	b := domain.NewBasket(domain.BasketID(s.newID()), cashierID, now)
	return b, s.baskets.Save(ctx, b)
}

// CurrentBasket returns the last basket the cashier can continue (when the screen or
// the service restarts); ErrNotFound if no basket changed within the last ResumeWindow.
// If the basket has gone to payment (CheckedOut) it looks at whether the sale was paid
// from sales via the caller.
func (s *Service) CurrentBasket(ctx context.Context, cashierID string) (*domain.Basket, error) {
	return s.baskets.Latest(ctx, cashierID, s.Now().Add(-ResumeWindow))
}

// Basket returns the basket.
func (s *Service) Basket(ctx context.Context, id domain.BasketID) (*domain.Basket, error) {
	return s.baskets.Get(ctx, id)
}

// AddBarcode adds the scanned barcode to the basket. With a scale barcode the product code and
// the weight are decoded from the barcode; other barcodes are added as 1 piece.
// If a weighed product is chosen without a scale barcode (a quick key, a product barcode)
// the weight is read from the scale at the register; if there is no scale a scale barcode is needed.
func (s *Service) AddBarcode(ctx context.Context, id domain.BasketID, barcode string) (*domain.Basket, error) {
	code := strings.TrimSpace(barcode)
	key, qty, weighed, err := domain.ParseScaleBarcode(code)
	if err != nil {
		return nil, err
	}
	if !weighed {
		key, qty = code, money.QtyScale
	}
	p, err := s.products.ByBarcode(ctx, key)
	if err != nil {
		return nil, err
	}
	// The weighing is outside the basket lock: while the scale settles the barcodes
	// coming from the scanner do not wait.
	if p.Unit == domain.Kg && !weighed && s.scale != nil {
		g, err := s.scale.Weigh(ctx)
		switch {
		case errors.Is(err, ErrNoScale):
		case err != nil:
			return nil, err
		default:
			qty, weighed = g, true
		}
	}
	return s.update(ctx, id, func(b *domain.Basket) error { return b.Add(p, qty, weighed) })
}

// SetQty changes the quantity of a line.
func (s *Service) SetQty(ctx context.Context, id domain.BasketID, line int, qty money.Qty) (*domain.Basket, error) {
	return s.update(ctx, id, func(b *domain.Basket) error { return b.SetQty(line, qty) })
}

// RemoveLine removes a line from the basket.
func (s *Service) RemoveLine(ctx context.Context, id domain.BasketID, line int) (*domain.Basket, error) {
	return s.update(ctx, id, func(b *domain.Basket) error { return b.Remove(line) })
}

// Checkout creates a sale in sales in the cashier's name with the basket's lines
// and ties the basket to that sale. The payment is taken in sales. If the basket is already
// tied to a sale the same sale is returned; a repeated request does not
// create a second sale.
func (s *Service) Checkout(ctx context.Context, id domain.BasketID, cashierID string) (*domain.Basket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.baskets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.Status == domain.CheckedOut {
		return b, nil
	}
	if len(b.Lines) == 0 {
		return nil, domain.ErrEmpty
	}
	saleID, err := s.sales.CreateSale(ctx, cashierID, b.Lines)
	if err != nil {
		return nil, err
	}
	if err := b.CheckOut(saleID); err != nil {
		return nil, err
	}
	b.UpdatedAt = s.Now()
	return b, s.baskets.Save(ctx, b)
}

// Reopen reopens a basket whose payment was abandoned. Cancelling the sale
// (VoidSale in sales) is the caller's responsibility.
func (s *Service) Reopen(ctx context.Context, id domain.BasketID) (*domain.Basket, error) {
	return s.update(ctx, id, func(b *domain.Basket) error {
		b.Reopen()
		return nil
	})
}

// Search looks for products; if the query is empty it returns the quick keys.
func (s *Service) Search(ctx context.Context, query string) ([]domain.Product, error) {
	return s.products.Search(ctx, query, 48)
}

// update reads the basket, changes it and saves it; changes that come at the same time
// are applied in order.
func (s *Service) update(ctx context.Context, id domain.BasketID, change func(*domain.Basket) error) (*domain.Basket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.baskets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := change(b); err != nil {
		return nil, err
	}
	b.UpdatedAt = s.Now()
	return b, s.baskets.Save(ctx, b)
}
