// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func valid() Product {
	return Product{Code: "ekmek", Name: "Ekmek 250 g", Unit: Piece, TaxRate: 100, Barcodes: []string{"8690000000012"}, Prices: []Price{{Amount: 1500}}}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Product){
		"kodda nokta":         func(p *Product) { p.Code = "ekmek.250" },
		"boş kod":             func(p *Product) { p.Code = "" },
		"boş ad":              func(p *Product) { p.Name = "" },
		"birim":               func(p *Product) { p.Unit = "litre" },
		"KDV":                 func(p *Product) { p.TaxRate = 10001 },
		"barkodda boşluk":     func(p *Product) { p.Barcodes = []string{"869 000"} },
		"fiyat yok":           func(p *Product) { p.Prices = nil },
		"baştan fiyat yok":    func(p *Product) { p.Prices = []Price{{Amount: 1, From: time.Now()}} },
		"negatif fiyat":       func(p *Product) { p.Prices = []Price{{Amount: -1}} },
		"aynı anda iki fiyat": func(p *Product) { p.Prices = []Price{{Amount: 1}, {Amount: 2}} },
	} {
		p := valid()
		mutate(&p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidProduct) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Normalize: name spaces, barcodes sorted and unique.
	p := Product{Code: " sut ", Name: "  Süt   1 L ", Barcodes: []string{"2", " 1", "2", ""}}.Normalize()
	if p.Code != "sut" || p.Name != "Süt 1 L" || !slices.Equal(p.Barcodes, []string{"1", "2"}) {
		t.Errorf("normalize: %+v", p)
	}
}

func TestSchedulePrice(t *testing.T) {
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	p := valid()

	p, err := p.SchedulePrice(1650, monday, now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Prices, []Price{{Amount: 1500}, {Amount: 1650, From: monday}}) {
		t.Fatalf("future-dated price: %+v", p.Prices)
	}
	if p.PriceAt(now) != 1500 || p.PriceAt(monday) != 1650 {
		t.Errorf("price in effect %v / %v", p.PriceAt(now), p.PriceAt(monday))
	}
	// A price given at the same moment replaces the earlier one.
	p, _ = p.SchedulePrice(1700, monday, now)
	if !slices.Equal(p.Prices, []Price{{Amount: 1500}, {Amount: 1700, From: monday}}) {
		t.Errorf("price at the same moment: %+v", p.Prices)
	}
	// An immediately valid price (a past date counts as immediate too): the current price
	// counts as from the beginning, the future record stays.
	p, _ = p.SchedulePrice(1550, now.Add(-time.Hour), now)
	if !slices.Equal(p.Prices, []Price{{Amount: 1550}, {Amount: 1700, From: monday}}) {
		t.Errorf("immediately valid price: %+v", p.Prices)
	}
	if _, err := p.SchedulePrice(-1, monday, now); !errors.Is(err, ErrInvalidProduct) {
		t.Errorf("negative price: %v", err)
	}

	// A future-dated price is withdrawn; one that has taken effect cannot be.
	p, err = p.CancelPrice(monday, now)
	if err != nil || !slices.Equal(p.Prices, []Price{{Amount: 1550}}) {
		t.Errorf("undo: %+v %v", p.Prices, err)
	}
	if _, err := p.CancelPrice(now.Add(-time.Minute), now); !errors.Is(err, ErrInvalidProduct) {
		t.Errorf("withdrawing the price in effect: %v", err)
	}
	if _, err := p.CancelPrice(monday, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("price that does not exist: %v", err)
	}
}
