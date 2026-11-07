// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package price is the price schedule: the headquarters price, the store exception and
// the future-dated price. The rule "which price is valid when" is written once
// here; the register (the price at the time of sale), the store server (the store prices
// screen) and headquarters (catalog management, products with different prices) use the same.
package price

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/money"
)

// Point is a record of the schedule: the VAT-included price valid starting from the moment
// From (from the beginning if From is zero; the price per kilogram for a weighed product). Clear
// is used only in the store schedule: the store exception is lifted at the moment From,
// and from that moment the headquarters price is valid.
type Point struct {
	Amount money.Money
	From   time.Time
	Clear  bool
}

var (
	// ErrInvalid means the price or the schedule is invalid.
	ErrInvalid = errors.New("fiyat geçersiz")
	// ErrStarted is an attempt to withdraw a record that has taken effect.
	ErrStarted = errors.New("yürürlüğe girmiş fiyat geri alınamaz")
	// ErrNotScheduled means there is no record at the moment to be withdrawn.
	ErrNotScheduled = errors.New("bu anda fiyat yok")
)

// Current returns the last record in effect at the moment t (it may be a removal
// record too); false if there is none.
func Current(points []Point, t time.Time) (Point, bool) {
	var best Point
	found := false
	for _, p := range points {
		if p.From.After(t) {
			continue
		}
		if !found || p.From.After(best.From) {
			best, found = p, true
		}
	}
	return best, found
}

// At is the price valid at the moment t; false if there is no record in effect or the last
// record is a removal.
func At(points []Point, t time.Time) (money.Money, bool) {
	p, ok := Current(points, t)
	if !ok || p.Clear {
		return 0, false
	}
	return p.Amount, true
}

// Effective is the price valid at sale at the moment t: the store price in effect
// (if not lifted) takes precedence over the headquarters price. store says the price is the
// store exception; if there is neither ok is false.
func Effective(headquarters, storePrices []Point, t time.Time) (amount money.Money, store, ok bool) {
	if a, ok := At(storePrices, t); ok {
		return a, true, true
	}
	a, ok := At(headquarters, t)
	return a, false, ok
}

// Upcoming are the records that will take effect after t (in schedule order).
func Upcoming(points []Point, t time.Time) []Point {
	var out []Point
	for _, p := range points {
		if p.From.After(t) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b Point) int { return a.From.Compare(b.From) })
	return out
}

// Plan adds the price valid from the moment from (from now if zero or in the past)
// to the schedule; if clear, the exception is lifted from that moment. A record given at
// the same moment is replaced by the new one; records left in the past are
// dropped (Prune).
func Plan(points []Point, amount money.Money, clear bool, from, now time.Time) ([]Point, error) {
	if !clear && amount < 0 {
		return nil, fmt.Errorf("%w: fiyat negatif", ErrInvalid)
	}
	if from.IsZero() || from.Before(now) {
		from = now
	}
	var out []Point
	for _, p := range points {
		if !p.From.Equal(from) {
			out = append(out, p)
		}
	}
	if clear {
		amount = 0
	}
	out = append(out, Point{Amount: amount, From: from, Clear: clear})
	return Prune(out, now), nil
}

// Cancel removes a record that has not taken effect yet from the schedule.
func Cancel(points []Point, from, now time.Time) ([]Point, error) {
	if !from.After(now) {
		return nil, ErrStarted
	}
	i := slices.IndexFunc(points, func(p Point) bool { return p.From.Equal(from) })
	if i < 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotScheduled, from.Format(time.RFC3339))
	}
	return slices.Delete(slices.Clone(points), i, i+1), nil
}

// Prune drops the records that can no longer take effect and sorts the schedule by
// effective moment: the price in effect now counts as from the beginning
// (if the record in effect is a removal it is dropped), the later ones stay.
func Prune(points []Point, now time.Time) []Point {
	sorted := slices.Clone(points)
	slices.SortStableFunc(sorted, func(a, b Point) int { return a.From.Compare(b.From) })
	var out []Point
	for _, p := range sorted {
		if p.From.After(now) {
			out = append(out, p)
			continue
		}
		out = nil
		if !p.Clear {
			out = []Point{{Amount: p.Amount}}
		}
	}
	return out
}

// Validate checks the schedule: the amount is not negative, there are not two records
// at the same moment.
func Validate(points []Point) error {
	seen := map[int64]bool{}
	for _, p := range points {
		if p.Amount < 0 {
			return fmt.Errorf("%w: fiyat negatif", ErrInvalid)
		}
		at := int64(0)
		if !p.From.IsZero() {
			at = p.From.UnixNano()
		}
		if seen[at] {
			return fmt.Errorf("%w: aynı anda iki fiyat", ErrInvalid)
		}
		seen[at] = true
	}
	return nil
}
