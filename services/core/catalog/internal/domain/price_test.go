// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"time"

	"abarow.com/pkg/money"
)

func TestPriceAt(t *testing.T) {
	ist, _ := time.LoadLocation("Europe/Istanbul")
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, ist)
	friday := time.Date(2026, 11, 6, 0, 0, 0, 0, ist)
	prices := []Price{
		{Source: HeadquartersPrice, Amount: 1500},
		{Source: HeadquartersPrice, Amount: 1650, From: monday},
		{Source: StorePrice, Amount: 1800, From: monday.Add(12 * time.Hour)},
		{Source: StorePrice, Clear: true, From: friday},
	}
	for _, tt := range []struct {
		at     time.Time
		amount money.Money
		source PriceSource
	}{
		{monday.Add(-time.Second), 1500, HeadquartersPrice}, // pazar 23:59:59: eski fiyat
		{monday, 1650, HeadquartersPrice},                   // tam 00:00'da yeni fiyat
		{monday.Add(12 * time.Hour), 1800, StorePrice},      // store exception
		{friday.Add(-time.Minute), 1800, StorePrice},        // the exception continues
		{friday, 1650, HeadquartersPrice},                   // the exception lifted: headquarters price
		{friday.AddDate(1, 0, 0), 1650, HeadquartersPrice},  // after that
	} {
		amount, source, err := PriceAt(prices, tt.at)
		if err != nil || amount != tt.amount || source != tt.source {
			t.Errorf("%s: %v %s %v; should be %v %s", tt.at, amount, source, err, tt.amount, tt.source)
		}
	}

	// A product whose sale starts at a future date has no price until then.
	future := []Price{{Source: HeadquartersPrice, Amount: 2500, From: monday}}
	if _, _, err := PriceAt(future, monday.Add(-time.Hour)); !errors.Is(err, ErrNoPrice) {
		t.Errorf("price not in effect: %v", err)
	}
	// Without a headquarters price the store price is enough.
	if amount, source, err := PriceAt([]Price{{Source: StorePrice, Amount: 900}}, monday); err != nil || amount != 900 || source != StorePrice {
		t.Errorf("store price only: %v %s %v", amount, source, err)
	}
}

func TestValidatePrices(t *testing.T) {
	now := time.Now()
	if err := ValidatePrices([]Price{{Source: HeadquartersPrice, Amount: 1}, {Source: HeadquartersPrice, Amount: 2, From: now}, {Source: StorePrice, Amount: 3, From: now}}); err != nil {
		t.Errorf("valid schedule: %v", err)
	}
	for _, bad := range [][]Price{
		{{Source: "kampanya", Amount: 1}},
		{{Source: HeadquartersPrice, Amount: -1}},
		{{Source: HeadquartersPrice, Clear: true, From: now}},
		{{Source: HeadquartersPrice, Amount: 1, From: now}, {Source: HeadquartersPrice, Amount: 2, From: now}},
		{{Source: HeadquartersPrice, Amount: 1}, {Source: HeadquartersPrice, Amount: 2}},
	} {
		if err := ValidatePrices(bad); !errors.Is(err, ErrInvalidProduct) {
			t.Errorf("%+v kabul edildi", bad)
		}
	}
}

func TestPrunePrices(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	prices := []Price{
		{Source: HeadquartersPrice, Amount: 1000},
		{Source: HeadquartersPrice, Amount: 1100, From: now.Add(-48 * time.Hour)},
		{Source: HeadquartersPrice, Amount: 1200, From: now.Add(24 * time.Hour)},
		{Source: StorePrice, Amount: 1300, From: now.Add(-72 * time.Hour)},
		{Source: StorePrice, Clear: true, From: now.Add(-time.Hour)},
	}
	got := PrunePrices(prices, now)
	want := []Price{prices[1], prices[4], prices[2]}
	if len(got) != len(want) {
		t.Fatalf("budanan takvim %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v; should be %+v", i, got[i], want[i])
		}
	}
	// Pruning does not change the current and future price.
	for _, at := range []time.Time{now, now.Add(25 * time.Hour)} {
		a, _, _ := PriceAt(prices, at)
		b, _, _ := PriceAt(got, at)
		if a != b {
			t.Errorf("%s: before pruning %v, after %v", at, a, b)
		}
	}
}
