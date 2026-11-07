// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package report is the report core shared by the store server and headquarters: the period
// summary limited to the stores the person sees (the figures from the sales ledger's
// report, pkg/ledger), the summary's JSON and the shared shell of the service that serves
// the screen (security headers, forwarding to the services behind).
//
// The store server with the "this store" scope and headquarters with the "the person's stores"
// scope use the same service; only the Source adapter changes
// (from which database and with which names the report is read). Tier-specific things such as the
// registers' state, cashiers and store prices are in the services that use this
// package.
package report

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
)

// Filter is the report's period and stores.
type Filter struct {
	// Start and End are the period's bounds ([Start, End)).
	Start, End time.Time
	// If Stores is nil it is all the stores; if empty, none.
	Stores []string
	// Location is the time zone in which the days are computed.
	Location *time.Location
	// MaxProducts is the largest number of products in the report; all if 0.
	MaxProducts int
}

// Result is the report Source returns: the ledger report and the names that will
// show on the screen.
type Result struct {
	ledger.Report
	// StoreNames is the mapping from store id to name.
	StoreNames map[string]string
	// CashierNames is the mapping from [store, cashier] id to name. The name of a cashier defined
	// only in the file at the register is not known; it is empty.
	CashierNames map[[2]string]string
}

// Source is where the report is read (the store server's and headquarters' database).
type Source interface {
	// Report returns the period's report and its names in a single, consistent read.
	Report(ctx context.Context, f Filter) (Result, error)
}

// Hatalar.
var (
	// ErrInvalidPeriod means the requested period is invalid.
	ErrInvalidPeriod = ledger.ErrInvalidPeriod
	// ErrForbidden means the person cannot see the requested store's reports.
	ErrForbidden = errors.New("bu mağazanın raporlarını görme yetkiniz yok")
)

// Query is the requested period. From and To (2006-01-02) are both included; today if
// From is empty, From if To is empty. All the stores the person sees if Store is empty.
type Query struct {
	From, To, Store string
}

// Summary is a period's sales summary. The days are computed in store time
// (ABAROW_TZ). It has no fiscal value; the fiscal reports (Z) are taken at the
// registers.
type Summary struct {
	// From and To are the first and last day of the period (2006-01-02; both included).
	From, To string
	// If StoreID is empty it is all the stores the person sees.
	StoreID string
	Result
	// registerName is the register's name shown on the screen; if nil the register
	// breakdown is not shown.
	registerName func(id string) string
}

// StoreName is the store's name (its id if none).
func (s Summary) StoreName(id string) string {
	if n := s.StoreNames[id]; n != "" {
		return n
	}
	return id
}

// CashierName is the cashier's name at the store (empty if not known).
func (s Summary) CashierName(store, id string) string { return s.CashierNames[[2]string{store, id}] }

// Service is the use case of the period summary.
type Service struct {
	src         Source
	loc         *time.Location
	now         func() time.Time
	single      bool
	maxProducts int
	registers   func(id string) string
}

// Option is Service's tier-specific setting.
type Option func(*Service)

// SingleStore says the service reads the data of a single store (the back
// house): the store filter is not used, the store in the request is ignored.
func SingleStore() Option { return func(s *Service) { s.single = true } }

// MaxProducts is the largest number of products in the summary (0: all).
func MaxProducts(n int) Option { return func(s *Service) { s.maxProducts = n } }

// RegisterNames adds the register breakdown to the summary; name gives the register's
// display name on the screen (store server: registers.json).
func RegisterNames(name func(id string) string) Option {
	return func(s *Service) { s.registers = name }
}

// New returns a service that computes days in loc time.
func New(src Source, loc *time.Location, opts ...Option) *Service {
	s := &Service{src: src, loc: loc, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetClock changes the service's clock (for tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Today is today's date in store time.
func (s *Service) Today() string { return s.now().In(s.loc).Format(time.DateOnly) }

// Summary returns the summary of the stores person p can see for period q.
// ErrForbidden for a store the person cannot see; ErrInvalidPeriod if the period is invalid.
func (s *Service) Summary(ctx context.Context, q Query, p login.Person) (Summary, error) {
	var stores []string // nil: all the stores
	storeID := ""
	if !s.single {
		storeID = q.Store
		switch {
		case storeID != "":
			if !sees(p, storeID) {
				return Summary{}, ErrForbidden
			}
			stores = []string{storeID}
		case !p.AllStores:
			stores = append([]string{}, p.Stores...)
		}
	}
	from, to := q.From, q.To
	if from == "" {
		from = s.Today()
	}
	if to == "" {
		to = from
	}
	start, end, err := ledger.Period(from, to, s.loc)
	if err != nil {
		return Summary{}, err
	}
	res, err := s.src.Report(ctx, Filter{Start: start, End: end, Stores: stores, Location: s.loc, MaxProducts: s.maxProducts})
	if err != nil {
		return Summary{}, fmt.Errorf("özet okunamadı: %w", err)
	}
	sum := Summary{From: from, To: to, StoreID: storeID, Result: res, registerName: s.registers}
	// Days with no sales are listed too.
	days := map[string]ledger.DayTotal{}
	for _, d := range sum.Days {
		days[d.Date] = d
	}
	sum.Days = nil
	for _, date := range ledger.Dates(start, end) {
		d := days[date]
		d.Date = date
		sum.Days = append(sum.Days, d)
	}
	return sum, nil
}

func sees(p login.Person, store string) bool { return p.AllStores || slices.Contains(p.Stores, store) }
