// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the staff service's use cases and ports.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"abarow.com/services/core/staff/internal/domain"
)

// Store keeps the periods and the sales that enter the report.
type Store interface {
	// SaveCompleted writes the sale to the open period; the same sale is not
	// written a second time (events are delivered at least once).
	SaveCompleted(ctx context.Context, s domain.CompletedSale) error
	// SaveVoided writes the cancelled sale to the open period; repeats are ignored.
	SaveVoided(ctx context.Context, v domain.VoidedSale) error
	// OpenPeriod returns the open period; opens one if there is no period.
	OpenPeriod(ctx context.Context, now time.Time) (domain.Period, error)
	// ClosePeriod closes the open period, opens a new one and returns the one closed.
	ClosePeriod(ctx context.Context, now time.Time) (domain.Period, error)
	// Facts returns the period's sales.
	Facts(ctx context.Context, zNo int64) ([]domain.CompletedSale, []domain.VoidedSale, error)
	// Period returns the period with the given Z number; domain.ErrUnknownPeriod if none.
	Period(ctx context.Context, zNo int64) (domain.Period, error)
}

// Service is the end-of-day use cases.
type Service struct {
	store    Store
	shifts   Shifts
	cashiers Cashiers
	events   Events
	now      func() time.Time
}

// NewService connects the ports. events waits for the pending sale events to be
// processed before the end of day.
func NewService(store Store, shifts Shifts, cashiers Cashiers, events Events) *Service {
	return &Service{store: store, shifts: shifts, cashiers: cashiers, events: events, now: time.Now}
}

// RecordCompleted processes the closed sale into the open period.
func (s *Service) RecordCompleted(ctx context.Context, sale domain.CompletedSale) error {
	return s.store.SaveCompleted(ctx, sale)
}

// RecordVoided processes the cancelled sale into the open period.
func (s *Service) RecordVoided(ctx context.Context, v domain.VoidedSale) error {
	return s.store.SaveVoided(ctx, v)
}

// XReport is the instant report of the open period; it does not close the period.
//
// The report is made of the events that have reached staff. Sales made while the
// message path is cut enter the report when the events come.
func (s *Service) XReport(ctx context.Context) (domain.DayReport, error) {
	p, err := s.store.OpenPeriod(ctx, s.now())
	if err != nil {
		return domain.DayReport{}, err
	}
	return s.report(ctx, p)
}

// CloseDay closes the open period and returns the Z report; a new period is opened.
// byID is the cashier closing the day and must be a manager. The day is not closed while
// a shift is open at the register: first the register is counted and the shift closed. The day is
// not closed before the pending sale events are processed; otherwise the sales would be
// written to the next day.
func (s *Service) CloseDay(ctx context.Context, byID string) (domain.DayReport, error) {
	by, err := s.cashiers.Cashier(ctx, byID)
	if err != nil {
		return domain.DayReport{}, err
	}
	if !by.CanCloseDay() {
		return domain.DayReport{}, domain.ErrCloseDayNotManager
	}
	sh, err := s.shifts.CurrentShift(ctx)
	if err == nil {
		return domain.DayReport{}, fmt.Errorf("gün sonu alınamaz: %w; önce kasa sayımıyla vardiyayı kapatın",
			domain.ShiftOpenError{CashierName: sh.CashierName})
	}
	if !errors.Is(err, domain.ErrNoShift) {
		return domain.DayReport{}, err
	}
	if err := s.events.WaitProcessed(ctx); err != nil {
		return domain.DayReport{}, err
	}
	p, err := s.store.ClosePeriod(ctx, s.now())
	if err != nil {
		return domain.DayReport{}, err
	}
	return s.report(ctx, p)
}

// DayReport is the report of the period with the given Z number (Z if closed).
func (s *Service) DayReport(ctx context.Context, zNo int64) (domain.DayReport, error) {
	p, err := s.store.Period(ctx, zNo)
	if err != nil {
		return domain.DayReport{}, err
	}
	return s.report(ctx, p)
}

func (s *Service) report(ctx context.Context, p domain.Period) (domain.DayReport, error) {
	if p.DetailsRemoved {
		// The totals would be zero: an error instead of a wrong report.
		return domain.DayReport{}, domain.ErrDetailsRemoved
	}
	completed, voided, err := s.store.Facts(ctx, p.ZNo)
	if err != nil {
		return domain.DayReport{}, err
	}
	return domain.BuildReport(p, completed, voided), nil
}
