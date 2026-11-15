// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the catalog service's use cases and ports.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/text"
	"abarow.com/services/core/catalog/internal/domain"
)

// ErrNotFound is returned when the product is not found.
var ErrNotFound = errors.New("ürün bulunamadı")

// MaxResults is the largest number of products a search returns.
const MaxResults = 100

// Repository stores the products.
type Repository interface {
	// ByBarcode returns the product that corresponds to the barcode; ErrNotFound if none.
	ByBarcode(ctx context.Context, barcode string) (domain.Product, error)
	// Search returns the products that have the search key (text.Fold) in their name.
	Search(ctx context.Context, key string, limit int) ([]domain.Product, error)
	// QuickKeys returns the quick key products.
	QuickKeys(ctx context.Context, limit int) ([]domain.Product, error)
	// Upsert adds or updates the products; the headquarters price schedule is
	// replaced with the given one, the store prices are kept. A product coming from the initial
	// file does not change a product that came from headquarters.
	Upsert(ctx context.Context, products []domain.Product, origin domain.Origin) error
	// SetStorePrices replaces the product's store price schedule with the given one;
	// ErrNotFound if the product does not exist.
	SetStorePrices(ctx context.Context, productID string, prices []domain.Price) error
	// Get returns the product; ErrNotFound if none or withdrawn from sale.
	Get(ctx context.Context, id string) (domain.Product, error)
	// Menu: the products that have a category (are on the menu).
	Menu(ctx context.Context) ([]domain.Product, error)
	// SaveImage writes the product's image; deletes it if Data is empty.
	SaveImage(ctx context.Context, img domain.Image) error
	// Image is the product's current image (the one whose digest holds the product's); ErrNotFound
	// if none.
	Image(ctx context.Context, productID string) (domain.Image, error)
}

// Service, katalog use case'leridir.
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService connects the ports.
func NewService(repo Repository) *Service { return &Service{repo: repo, now: time.Now} }

// GetByBarcode returns the product that corresponds to the barcode with the price in
// effect right now. If the product's price is not in effect yet (e.g. its sale will start at a
// future date) domain.ErrNoPrice is returned.
func (s *Service) GetByBarcode(ctx context.Context, barcode string) (domain.Product, error) {
	p, err := s.repo.ByBarcode(ctx, strings.TrimSpace(barcode))
	if err != nil {
		return domain.Product{}, err
	}
	if p.UnitPrice, _, err = domain.PriceAt(p.Prices, s.now()); err != nil {
		return domain.Product{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	return p, nil
}

// Search returns the products that have the query in their name with the prices in effect;
// the quick keys if the query is empty. Products whose price is not in effect yet
// are not listed.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]domain.Product, error) {
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	var products []domain.Product
	var err error
	if key := text.Fold(query); key == "" {
		products, err = s.repo.QuickKeys(ctx, limit)
	} else {
		products, err = s.repo.Search(ctx, key, limit)
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := products[:0]
	for _, p := range products {
		if p.UnitPrice, _, err = domain.PriceAt(p.Prices, now); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// GetProduct returns the product with the price in effect.
func (s *Service) GetProduct(ctx context.Context, id string) (domain.Product, error) {
	p, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.Product{}, err
	}
	if p.UnitPrice, _, err = domain.PriceAt(p.Prices, s.now()); err != nil {
		return domain.Product{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	return p, nil
}

// Menu returns the products on the menu with the prices in effect in menu order;
// a product whose price is not in effect yet does not show on the menu.
func (s *Service) Menu(ctx context.Context) ([]domain.Product, error) {
	products, err := s.repo.Menu(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := products[:0]
	for _, p := range products {
		if p.UnitPrice, _, err = domain.PriceAt(p.Prices, now); err == nil {
			out = append(out, p)
		}
	}
	domain.SortMenu(out)
	return out, nil
}

// Image is the product's image.
func (s *Service) Image(ctx context.Context, productID string) (domain.Image, error) {
	return s.repo.Image(ctx, productID)
}

// ApplyImage applies the image coming from headquarters (deletes it if empty). The image
// is checked from its content; the type and digest are recomputed.
func (s *Service) ApplyImage(ctx context.Context, img domain.Image) error {
	if img.ProductID == "" {
		return fmt.Errorf("%w: görselin ürünü yok", domain.ErrInvalidProduct)
	}
	if len(img.Data) > 0 {
		ct, sum, err := menu.CheckImage(img.Data)
		if err != nil {
			return fmt.Errorf("%w (%s): %w", domain.ErrInvalidProduct, img.ProductID, err)
		}
		img.ContentType, img.SHA256 = ct, sum
	}
	return s.repo.SaveImage(ctx, img)
}

// Seed validates and loads the products. Existing products are updated; the headquarters
// price schedule is replaced with the loaded one (records left in the past are dropped).
// For a product with no schedule the UnitPrice is the headquarters price valid from the beginning.
func (s *Service) Seed(ctx context.Context, products []domain.Product) error {
	now := s.now()
	out := make([]domain.Product, len(products))
	for i, p := range products {
		if len(p.Prices) == 0 {
			p.Prices = []domain.Price{{Source: domain.HeadquartersPrice, Amount: p.UnitPrice}}
		}
		if err := p.Validate(); err != nil {
			return err
		}
		p.Prices = domain.PrunePrices(p.Prices, now)
		out[i] = p
	}
	return s.repo.Upsert(ctx, out, domain.OriginSeed)
}

// ApplyStorePrices applies the store price schedule coming from the store server (the store
// exception); an empty schedule lifts the exception. If the product has not come down to the
// register yet it returns ErrNotFound (the price comes again later).
func (s *Service) ApplyStorePrices(ctx context.Context, productID string, prices []domain.Price) error {
	for _, p := range prices {
		if p.Source != domain.StorePrice {
			return fmt.Errorf("%w: mağaza takviminde %s fiyatı", domain.ErrInvalidProduct, p.Source)
		}
	}
	if err := domain.ValidatePrices(prices); err != nil {
		return err
	}
	return s.repo.SetStorePrices(ctx, productID, domain.PrunePrices(prices, s.now()))
}

// Apply applies the latest state of a product coming from headquarters' catalog stream
// (if withdrawn from sale it is not found at the register). Price records left in the
// past are dropped.
func (s *Service) Apply(ctx context.Context, p domain.Product) error {
	if err := p.Validate(); err != nil {
		return err
	}
	p.Prices = domain.PrunePrices(p.Prices, s.now())
	return s.repo.Upsert(ctx, []domain.Product{p}, domain.OriginHeadquarters)
}
