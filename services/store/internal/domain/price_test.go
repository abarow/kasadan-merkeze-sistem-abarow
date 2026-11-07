// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestStorePriceSchedule(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	friday := monday.Add(96 * time.Hour)

	// An exception valid immediately, a new price on Monday, the exception lifted on Friday.
	points, err := ScheduleStorePrice(nil, 1800, false, time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	points, _ = ScheduleStorePrice(points, 1900, false, monday, now)
	points, _ = ScheduleStorePrice(points, 0, true, friday, now)
	want := []PricePoint{{Amount: 1800}, {Amount: 1900, From: monday}, {From: friday, Clear: true}}
	if !slices.Equal(points, want) {
		t.Fatalf("schedule %+v; should be %+v", points, want)
	}
	for _, tt := range []struct {
		at     time.Time
		amount int64
		ok     bool
	}{
		{now, 1800, true},
		{monday, 1900, true},
		{friday.Add(-time.Second), 1900, true},
		{friday, 0, false},
	} {
		amount, ok := StorePriceAt(points, tt.at)
		if ok != tt.ok || int64(amount) != tt.amount {
			t.Errorf("%s: %v %v", tt.at, amount, ok)
		}
	}
	if up := Upcoming(points, now); len(up) != 2 {
		t.Errorf("future records %+v", up)
	}

	// When Friday passes the schedule is pruned: no exception is left.
	if got := PruneStorePrices(points, friday); len(got) != 0 {
		t.Errorf("budanan takvim %+v", got)
	}
	// Between Monday and Friday: the price in effect counts as from the beginning.
	if got := PruneStorePrices(points, monday); !slices.Equal(got, []PricePoint{{Amount: 1900}, {From: friday, Clear: true}}) {
		t.Errorf("budanan takvim %+v", got)
	}

	// A future-dated record is withdrawn; one that has taken effect cannot be.
	points, err = CancelStorePrice(points, monday, now)
	if err != nil || !slices.Equal(points, []PricePoint{{Amount: 1800}, {From: friday, Clear: true}}) {
		t.Errorf("geri alma: %+v %v", points, err)
	}
	if _, err := CancelStorePrice(points, now.Add(-time.Hour), now); !errors.Is(err, ErrInvalidPrice) {
		t.Errorf("past record: %v", err)
	}
	if _, err := ScheduleStorePrice(nil, -1, false, monday, now); !errors.Is(err, ErrInvalidPrice) {
		t.Errorf("negatif fiyat: %v", err)
	}

	p := CatalogProduct{HeadquartersPrices: []PricePoint{{Amount: 1500}, {Amount: 1600, From: monday}}}
	if a, ok := p.HeadquartersPriceAt(now); !ok || a != 1500 {
		t.Errorf("headquarters price %v", a)
	}
	if a, _ := p.HeadquartersPriceAt(monday); a != 1600 {
		t.Errorf("headquarters price on Monday %v", a)
	}
}
