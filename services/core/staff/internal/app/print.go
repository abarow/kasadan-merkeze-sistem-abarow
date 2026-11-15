// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"time"

	"abarow.com/services/core/staff/internal/domain"
)

// Printer prints the reports on the receipt printer (the device service).
type Printer interface {
	// Columns is the printer's column count; the report is laid out at this width.
	Columns(ctx context.Context) (int, error)
	Print(ctx context.Context, title string, lines []string) error
}

// PrintService prints the X/Z and shift reports.
type PrintService struct {
	reports *Service
	shifts  *ShiftService
	printer Printer
	loc     *time.Location
	// Now returns the current time; it can be changed in tests.
	Now func() time.Time
}

// NewPrintService connects the report services to the printer; times are printed
// in loc.
func NewPrintService(reports *Service, shifts *ShiftService, printer Printer, loc *time.Location) *PrintService {
	return &PrintService{reports: reports, shifts: shifts, printer: printer, loc: loc, Now: time.Now}
}

// PrintDay prints the X report of the open period (zNo 0) or the Z report of a closed
// period.
func (p *PrintService) PrintDay(ctx context.Context, zNo int64) error {
	var (
		r   domain.DayReport
		err error
	)
	if zNo == 0 {
		r, err = p.reports.XReport(ctx)
	} else {
		r, err = p.reports.DayReport(ctx, zNo)
	}
	if err != nil {
		return err
	}
	columns, err := p.printer.Columns(ctx)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("X raporu (Z no %d)", r.ZNo)
	if !r.ClosedAt.IsZero() {
		title = fmt.Sprintf("Z raporu %d", r.ZNo)
	}
	return p.printer.Print(ctx, title, domain.RenderDayReport(r, columns, p.loc, p.Now()))
}

// PrintShift prints the report of a closed shift; the permission to view it is the same
// as ShiftService.Report's.
func (p *PrintService) PrintShift(ctx context.Context, viewerID string, shiftID int64) error {
	r, err := p.shifts.Report(ctx, viewerID, shiftID)
	if err != nil {
		return err
	}
	columns, err := p.printer.Columns(ctx)
	if err != nil {
		return err
	}
	return p.printer.Print(ctx, fmt.Sprintf("Vardiya raporu %d", r.ID), domain.RenderShiftReport(r, columns, p.loc, p.Now()))
}
