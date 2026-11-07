// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app is the application layer of the headquarters reports: the stores'
// state, the store prices and the screen sign-in. The period summary is in the shared report
// core (pkg/report).
package app

import (
	"context"
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"abarow.com/pkg/ledger"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// Reports is the read side of the headquarters database (ingest writes the
// events).
type Reports interface {
	// Stores returns the stores' state and their sales in the range [dayStart, dayEnd)
	// (sorted by store id).
	Stores(ctx context.Context, dayStart, dayEnd time.Time) ([]domain.StoreOverview, error)
	// StorePrices are the price schedules the stores reported (all products if productID is
	// empty), with the products' headquarters schedules.
	StorePrices(ctx context.Context, productID string) ([]domain.StorePriceRow, error)
}

// Service holds the use cases of the reports.
type Service struct {
	reports Reports
	loc     *time.Location
	now     func() time.Time
}

// NewService returns a service that computes days in loc time.
func NewService(reports Reports, loc *time.Location) *Service {
	return &Service{reports: reports, loc: loc, now: time.Now}
}

// Today is today's date in store time.
func (s *Service) Today() string { return s.now().In(s.loc).Format(time.DateOnly) }

// Stores returns the state of the stores the user sees and their sales today.
func (s *Service) Stores(ctx context.Context, u domain.User) ([]domain.StoreOverview, error) {
	start, end, err := ledger.Period(s.Today(), s.Today(), s.loc)
	if err != nil {
		return nil, err
	}
	stores, err := s.reports.Stores(ctx, start, end)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var out []domain.StoreOverview
	for _, st := range stores {
		if u.SeesStore(st.ID) {
			out = append(out, st.Freshen(now))
		}
	}
	return out, nil
}

// StorePrices are the products priced differently from the headquarters price at the stores the user
// sees (now or at a future date; all products if productID is empty):
// sorted by store, then by product name.
func (s *Service) StorePrices(ctx context.Context, productID string, u domain.User) ([]domain.PriceException, error) {
	rows, err := s.reports.StorePrices(ctx, productID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var out []domain.PriceException
	for _, r := range rows {
		if !u.SeesStore(r.StoreID) {
			continue
		}
		if e, ok := r.At(now); ok {
			out = append(out, e)
		}
	}
	col := collate.New(language.Turkish)
	slices.SortStableFunc(out, func(a, b domain.PriceException) int {
		if c := col.CompareString(a.StoreName, b.StoreName); c != 0 {
			return c
		}
		return col.CompareString(a.ProductName, b.ProductName)
	})
	return out, nil
}
