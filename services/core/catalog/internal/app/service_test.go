// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"abarow.com/services/core/catalog/internal/domain"
)

// memRepo is the Repository that keeps the products in memory.
type memRepo struct{ products map[string]domain.Product }

func (m *memRepo) ByBarcode(_ context.Context, barcode string) (domain.Product, error) {
	for _, p := range m.products {
		for _, b := range p.Barcodes {
			if b == barcode {
				return p, nil
			}
		}
	}
	return domain.Product{}, ErrNotFound
}

func (m *memRepo) Search(_ context.Context, _ string, _ int) ([]domain.Product, error) {
	var out []domain.Product
	for _, id := range []string{"ekmek", "yeni"} {
		if p, ok := m.products[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memRepo) QuickKeys(ctx context.Context, limit int) ([]domain.Product, error) {
	return m.Search(ctx, "", limit)
}

func (m *memRepo) SetStorePrices(_ context.Context, id string, prices []domain.Price) error {
	p, ok := m.products[id]
	if !ok {
		return ErrNotFound
	}
	var kept []domain.Price
	for _, pr := range p.Prices {
		if pr.Source == domain.HeadquartersPrice {
			kept = append(kept, pr)
		}
	}
	p.Prices = append(kept, prices...)
	m.products[id] = p
	return nil
}

func (m *memRepo) Upsert(_ context.Context, products []domain.Product, _ domain.Origin) error {
	for _, p := range products {
		m.products[p.ID] = p
	}
	return nil
}

func TestPriceInEffect(t *testing.T) {
	ctx := context.Background()
	ist, _ := time.LoadLocation("Europe/Istanbul")
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, ist)
	repo := &memRepo{products: map[string]domain.Product{}}
	svc := NewService(repo)
	now := monday.Add(-48 * time.Hour)
	svc.now = func() time.Time { return now }

	err := svc.Seed(ctx, []domain.Product{
		{ID: "ekmek", Name: "Ekmek", Unit: domain.Piece, Barcodes: []string{"1"},
			Prices: []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1500}, {Source: domain.HeadquartersPrice, Amount: 1650, From: monday}}},
		// A product whose sale starts on Monday.
		{ID: "yeni", Name: "Yeni Ürün", Unit: domain.Piece, Barcodes: []string{"2"},
			Prices: []domain.Price{{Source: domain.HeadquartersPrice, Amount: 2500, From: monday}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	now = monday.Add(-time.Second)
	if p, err := svc.GetByBarcode(ctx, "1"); err != nil || p.UnitPrice != 1500 {
		t.Errorf("pazar 23:59:59: %v %v", p.UnitPrice, err)
	}
	if _, err := svc.GetByBarcode(ctx, "2"); !errors.Is(err, domain.ErrNoPrice) {
		t.Errorf("product whose sale has not started: %v", err)
	}
	if list, _ := svc.Search(ctx, "", 10); len(list) != 1 || list[0].ID != "ekmek" {
		t.Errorf("a product whose sale has not started was listed: %+v", list)
	}

	now = monday
	if p, err := svc.GetByBarcode(ctx, "1"); err != nil || p.UnitPrice != 1650 {
		t.Errorf("pazartesi 00:00: %v %v", p.UnitPrice, err)
	}
	if list, _ := svc.Search(ctx, "", 10); len(list) != 2 || list[1].UnitPrice != 2500 {
		t.Errorf("pazartesi listesi: %+v", list)
	}

	// The price of a product with no schedule is the headquarters price valid from the beginning.
	if err := svc.Seed(ctx, []domain.Product{{ID: "su", Name: "Su", Unit: domain.Piece, UnitPrice: 700, Barcodes: []string{"3"}}}); err != nil {
		t.Fatal(err)
	}
	if p, err := svc.GetByBarcode(ctx, "3"); err != nil || p.UnitPrice != 700 {
		t.Errorf("product with no schedule: %v %v", p.UnitPrice, err)
	}
	// Records left in the past are dropped on loading.
	if err := svc.Seed(ctx, []domain.Product{repo.products["ekmek"]}); err != nil {
		t.Fatal(err)
	}
	if got := repo.products["ekmek"].Prices; len(got) != 1 || got[0].Amount != 1650 {
		t.Errorf("budanan takvim %+v", got)
	}
}

func TestStorePrices(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	repo := &memRepo{products: map[string]domain.Product{}}
	svc := NewService(repo)
	svc.now = func() time.Time { return now }
	if err := svc.Seed(ctx, []domain.Product{{ID: "ekmek", Name: "Ekmek", Unit: domain.Piece, UnitPrice: 1500, Barcodes: []string{"1"}}}); err != nil {
		t.Fatal(err)
	}
	// Store exception: 18.00 TL from tomorrow 00:00.
	tomorrow := now.Add(12 * time.Hour)
	if err := svc.ApplyStorePrices(ctx, "ekmek", []domain.Price{{Source: domain.StorePrice, Amount: 1800, From: tomorrow}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.GetByBarcode(ctx, "1"); p.UnitPrice != 1500 {
		t.Errorf("exception before it takes effect: %v", p.UnitPrice)
	}
	now = tomorrow
	if p, _ := svc.GetByBarcode(ctx, "1"); p.UnitPrice != 1800 {
		t.Errorf("exception in effect: %v", p.UnitPrice)
	}
	// An empty schedule lifts the exception.
	if err := svc.ApplyStorePrices(ctx, "ekmek", nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := svc.GetByBarcode(ctx, "1"); p.UnitPrice != 1500 {
		t.Errorf("exception lifted: %v", p.UnitPrice)
	}
	if err := svc.ApplyStorePrices(ctx, "yok", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-existent product: %v", err)
	}
	if err := svc.ApplyStorePrices(ctx, "ekmek", []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1}}); !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("headquarters price in the store schedule: %v", err)
	}
}

func (m *memRepo) Get(_ context.Context, id string) (domain.Product, error) {
	if p, ok := m.products[id]; ok && !p.Discontinued {
		return p, nil
	}
	return domain.Product{}, ErrNotFound
}

func (m *memRepo) Menu(context.Context) ([]domain.Product, error) {
	var out []domain.Product
	for _, p := range m.products {
		if p.Category != "" && !p.Discontinued {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memRepo) SaveImage(context.Context, domain.Image) error { return nil }

func (m *memRepo) Image(context.Context, string) (domain.Image, error) {
	return domain.Image{}, ErrNotFound
}
