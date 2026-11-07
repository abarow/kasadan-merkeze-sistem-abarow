// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/services/core/staff/internal/domain"
)

// Shifts keeps the shifts. Sales are tied to the shift when recorded
// (see Store.SaveCompleted).
type Shifts interface {
	// CreateShift records the shift and gives its number. If there is an open shift at the
	// register it returns domain.ShiftOpenError; the check and the record are
	// atomic. If announce is not nil the numbered shift's event is written to the
	// outbox in the same transaction.
	CreateShift(ctx context.Context, s domain.Shift, announce func(domain.Shift) (outbox.Message, error)) (domain.Shift, error)
	// CurrentShift returns the open shift at the register; domain.ErrNoShift if none.
	CurrentShift(ctx context.Context) (domain.Shift, error)
	// CloseShift records the shift's closing; domain.ErrNoShift if the shift is no
	// longer open. If msg is not nil the closing event is written to the outbox in the
	// same transaction.
	CloseShift(ctx context.Context, s domain.Shift, msg *outbox.Message) error
	// ShiftFacts returns the sales that enter the shift.
	ShiftFacts(ctx context.Context, shiftID int64) ([]domain.CompletedSale, []domain.VoidedSale, error)
	// AddCashMovement records the cash movement and gives its number;
	// domain.ErrNoShift if the shift is no longer open. The check and the record
	// are atomic: no movement is written to a closed shift.
	AddCashMovement(ctx context.Context, m domain.CashMovement) (domain.CashMovement, error)
	// CashMovements returns the shift's cash movements in the order recorded.
	CashMovements(ctx context.Context, shiftID int64) ([]domain.CashMovement, error)
	// Shift returns the shift; domain.ErrUnknownShift if none.
	Shift(ctx context.Context, id int64) (domain.Shift, error)
	// ClosedShifts returns the closed shifts from newest to oldest; if cashierID is not
	// empty only that cashier's.
	ClosedShifts(ctx context.Context, cashierID string, limit int) ([]domain.Shift, error)
	// AddDrawerOpen records opening the drawer without a sale; domain.ErrNoShift if the shift
	// is no longer open.
	AddDrawerOpen(ctx context.Context, d domain.DrawerOpen) (domain.DrawerOpen, error)
	// DrawerOpens are the shift's drawer openings without a sale.
	DrawerOpens(ctx context.Context, shiftID int64) ([]domain.DrawerOpen, error)
}

// ShiftEvents turns the shift events (shift.opened.v1, shift.closed.v1) into
// an outbox message (see eventproto). The events are written in the same transaction
// as the shift record, the relay passes them on to the message path.
type ShiftEvents interface {
	Opened(sh domain.Shift) (outbox.Message, error)
	Closed(r domain.ShiftReport) (outbox.Message, error)
}

// Drawer is the cash drawer (the device service).
type Drawer interface {
	Open(ctx context.Context, reason string) error
}

// ErrNoDrawer is returned when opening the drawer is requested without the drawer being configured.
var ErrNoDrawer = errors.New("çekmece yapılandırılmamış (ABAROW_DEVICE_ADDR)")

// ShiftService, vardiya use case'leridir.
type ShiftService struct {
	shifts   Shifts
	cashiers Cashiers
	events   Events
	// Drawer is used when opening without a sale; if nil the opening returns ErrNoDrawer.
	Drawer Drawer
	// Announcer produces the shift events; if nil no event is written.
	Announcer ShiftEvents
	// Now returns the current time; it can be changed in tests.
	Now func() time.Time
}

// NewShiftService connects the ports. events waits for the pending sale events to be
// processed before closing.
func NewShiftService(shifts Shifts, cashiers Cashiers, events Events) *ShiftService {
	return &ShiftService{shifts: shifts, cashiers: cashiers, events: events, Now: time.Now}
}

// Current returns the open shift at the register; domain.ErrNoShift if none.
func (s *ShiftService) Current(ctx context.Context) (domain.Shift, error) {
	return s.shifts.CurrentShift(ctx)
}

// Open opens the cashier's shift with the opening cash.
func (s *ShiftService) Open(ctx context.Context, cashierID string, openingCash money.Money) (domain.Shift, error) {
	c, err := s.cashiers.Cashier(ctx, cashierID)
	if err != nil {
		return domain.Shift{}, err
	}
	sh, err := domain.NewShift(*c, openingCash, s.Now())
	if err != nil {
		return domain.Shift{}, err
	}
	var announce func(domain.Shift) (outbox.Message, error)
	if s.Announcer != nil {
		announce = s.Announcer.Opened
	}
	return s.shifts.CreateShift(ctx, sh, announce)
}

// Close closes the open shift with the counted cash and returns the shift report.
// byID is the cashier closing (the shift's owner or a manager).
//
// The report is made of the events. The shift is not closed before the events of the sales
// up to the closing moment are processed; otherwise the expected cash would come out short and
// the count would show a false cash surplus.
func (s *ShiftService) Close(ctx context.Context, byID string, countedCash money.Money) (domain.ShiftReport, error) {
	by, err := s.cashiers.Cashier(ctx, byID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	sh, err := s.shifts.CurrentShift(ctx)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	if err := sh.Close(*by, countedCash, s.Now()); err != nil {
		return domain.ShiftReport{}, err
	}
	if err := s.events.WaitProcessed(ctx); err != nil {
		return domain.ShiftReport{}, err
	}
	// The report is produced before the closing: the closing event carries the report, and if the
	// report cannot be produced the shift does not close.
	r, err := s.report(ctx, sh)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	var msg *outbox.Message
	if s.Announcer != nil {
		m, err := s.Announcer.Closed(r)
		if err != nil {
			return domain.ShiftReport{}, err
		}
		msg = &m
	}
	if err := s.shifts.CloseShift(ctx, sh, msg); err != nil {
		return domain.ShiftReport{}, err
	}
	return r, nil
}

// History returns the closed shifts from newest to oldest. A manager sees all the shifts, a
// cashier only their own shifts.
func (s *ShiftService) History(ctx context.Context, viewerID string, limit int) ([]domain.Shift, error) {
	viewer, err := s.cashiers.Cashier(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	only := viewer.ID
	if viewer.Role == domain.RoleManager {
		only = ""
	}
	return s.shifts.ClosedShifts(ctx, only, min(max(limit, 1), 100))
}

// Report returns the closed shift's report. The report is recomputed from the recorded sales
// and cash movements. The report of an open shift is not given (blind
// count).
func (s *ShiftService) Report(ctx context.Context, viewerID string, shiftID int64) (domain.ShiftReport, error) {
	viewer, err := s.cashiers.Cashier(ctx, viewerID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	sh, err := s.shifts.Shift(ctx, shiftID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	if !sh.CanViewReport(*viewer) {
		return domain.ShiftReport{}, domain.ErrNotShiftOwner
	}
	return s.report(ctx, sh)
}

// OpenDrawer opens the drawer without a sale and records it in the shift. byID is the manager
// who opens (with manager approval in a cashier session), requestedBy is the
// cashier in the session.
func (s *ShiftService) OpenDrawer(ctx context.Context, byID, requestedBy, reason string) (domain.DrawerOpen, error) {
	by, err := s.cashiers.Cashier(ctx, byID)
	if err != nil {
		return domain.DrawerOpen{}, err
	}
	sh, err := s.shifts.CurrentShift(ctx)
	if err != nil {
		return domain.DrawerOpen{}, err
	}
	d, err := sh.RecordDrawerOpen(*by, requestedBy, reason, s.Now())
	if err != nil {
		return domain.DrawerOpen{}, err
	}
	if s.Drawer == nil {
		return domain.DrawerOpen{}, ErrNoDrawer
	}
	if err := s.Drawer.Open(ctx, "satışsız açılış: "+d.Reason); err != nil {
		return domain.DrawerOpen{}, err
	}
	return s.shifts.AddDrawerOpen(ctx, d)
}

func (s *ShiftService) report(ctx context.Context, sh domain.Shift) (domain.ShiftReport, error) {
	completed, voided, err := s.shifts.ShiftFacts(ctx, sh.ID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	movements, err := s.shifts.CashMovements(ctx, sh.ID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	opens, err := s.shifts.DrawerOpens(ctx, sh.ID)
	if err != nil {
		return domain.ShiftReport{}, err
	}
	return domain.BuildShiftReport(sh, completed, voided, movements, opens)
}

// RecordCashMovement records a cash movement (putting in or taking out money) in the open
// shift. byID is the cashier recording; only a manager can take money
// out.
func (s *ShiftService) RecordCashMovement(ctx context.Context, byID string, kind domain.CashMovementKind, amount money.Money, note string) (domain.CashMovement, error) {
	by, err := s.cashiers.Cashier(ctx, byID)
	if err != nil {
		return domain.CashMovement{}, err
	}
	sh, err := s.shifts.CurrentShift(ctx)
	if err != nil {
		return domain.CashMovement{}, err
	}
	m, err := sh.RecordCashMovement(*by, kind, amount, note, s.Now())
	if err != nil {
		return domain.CashMovement{}, err
	}
	return s.shifts.AddCashMovement(ctx, m)
}
