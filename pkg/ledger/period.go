// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"errors"
	"fmt"
	"time"
)

// MaxPeriodDays is the longest period that can be requested in a report.
const MaxPeriodDays = 366

// ErrInvalidPeriod means the requested period is invalid.
var ErrInvalidPeriod = errors.New("dönem geçersiz")

// Period returns the start of the period's first day and the start of the day after its last day
// in store time (days are 2006-01-02). A day can last 23
// or 25 hours at a daylight saving change. The store server's daily report (a single day) and headquarters'
// period summary use the same calculation.
func Period(from, to string, loc *time.Location) (start, end time.Time, err error) {
	start, err = time.ParseInLocation(time.DateOnly, from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: başlangıç %q (ör. 2026-10-01)", ErrInvalidPeriod, from)
	}
	last, err := time.ParseInLocation(time.DateOnly, to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: bitiş %q (ör. 2026-10-31)", ErrInvalidPeriod, to)
	}
	if last.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: bitiş başlangıçtan önce", ErrInvalidPeriod)
	}
	end = last.AddDate(0, 0, 1)
	if start.AddDate(0, 0, MaxPeriodDays).Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: en çok %d gün seçilebilir", ErrInvalidPeriod, MaxPeriodDays)
	}
	return start, end, nil
}

// Day returns the start of a single day and the start of the next day (Period(date, date)).
func Day(date string, loc *time.Location) (start, end time.Time, err error) {
	return Period(date, date, loc)
}

// Dates are all the days of the period (2006-01-02).
func Dates(start, end time.Time) []string {
	var out []string
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}
