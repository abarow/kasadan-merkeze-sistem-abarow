// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"abarow.com/pkg/money"
)

var (
	ErrNoShift        = errors.New("açık vardiya yok")
	ErrNotShiftOwner  = errors.New("bu işlemi yalnızca vardiyanın sahibi ya da müdür yapabilir")
	ErrNegativeCash   = errors.New("nakit tutarı eksi olamaz")
	ErrShiftNotClosed = errors.New("vardiya henüz kapanmadı; rapor kapanıştan sonra görülür")
	ErrUnknownShift   = errors.New("vardiya bulunamadı")
	ErrCashAmount     = errors.New("tutar sıfırdan büyük olmalı")
	ErrCashNote       = errors.New("açıklama gerekli (en çok 200 karakter)")
	ErrCashKind       = errors.New("bilinmeyen kasa hareketi")
	// ErrCashOutNotManager: if a cashier could record a withdrawal in their own name they could
	// hide the shortage that would show up in the blind count.
	ErrCashOutNotManager = errors.New("kasadan para almak için müdür yetkisi gerekli")
	// ErrDrawerNotManager: opening without a sale is monitored for loss prevention.
	ErrDrawerNotManager = errors.New("çekmeceyi satış olmadan açmak için müdür yetkisi gerekli")
	ErrDrawerReason     = errors.New("çekmeceyi açma nedeni gerekli (en çok 200 karakter)")
)

// ShiftOpenError is returned when opening a new shift or taking the end of day is
// attempted while a shift is open at the register. There is a single drawer at the
// register; only one shift can be open at a time.
type ShiftOpenError struct {
	CashierName string
}

func (e ShiftOpenError) Error() string {
	return fmt.Sprintf("kasada açık vardiya var (%s)", e.CashierName)
}

// Shift is a cashier's working period at the register. A sale enters the shift if its
// cashier is the shift's owner and it was made while the shift was open.
type Shift struct {
	ID          int64
	CashierID   string
	CashierName string
	OpenedAt    time.Time
	// ClosedAt is zero in an open shift.
	ClosedAt time.Time
	// OpeningCash is the cash in the drawer at the start of the shift.
	OpeningCash money.Money
	// CountedCash is the cash counted at closing.
	CountedCash money.Money
	// ClosedBy is the cashier who closed the shift (the owner or a manager).
	ClosedBy string
}

// NewShift starts the cashier's shift with the opening cash.
func NewShift(c Cashier, openingCash money.Money, now time.Time) (Shift, error) {
	if openingCash < 0 {
		return Shift{}, ErrNegativeCash
	}
	return Shift{CashierID: c.ID, CashierName: c.Name, OpenedAt: now, OpeningCash: openingCash}, nil
}

// Open says whether the shift is open.
func (s Shift) Open() bool { return s.ClosedAt.IsZero() }

// Close closes the shift with the counted cash. The shift can be closed by its owner or a
// manager.
func (s *Shift) Close(by Cashier, countedCash money.Money, now time.Time) error {
	if !s.Open() {
		return ErrNoShift
	}
	if by.ID != s.CashierID && by.Role != RoleManager {
		return ErrNotShiftOwner
	}
	if countedCash < 0 {
		return ErrNegativeCash
	}
	s.ClosedAt = now
	s.CountedCash = countedCash
	s.ClosedBy = by.ID
	return nil
}

// CashMovementKind is cash that enters or leaves the drawer apart from sales.
type CashMovementKind string

const (
	// CashIn is putting money into the register (e.g. small change).
	CashIn CashMovementKind = "in"
	// CashOut is taking money out of the register (e.g. taking interim cash to the
	// safe, paying an expense).
	CashOut CashMovementKind = "out"
)

// CashMovement is cash that enters or leaves the drawer apart from sales in an open
// shift. It is not a sale, it needs no fiscal document; an advance payment taken from
// a customer (an advance receipt) is not this.
type CashMovement struct {
	ID      int64
	ShiftID int64
	Kind    CashMovementKind
	// Amount is always positive; Kind determines the direction.
	Amount money.Money
	Note   string
	// By, hareketi kaydeden kasiyerdir.
	By string
	At time.Time
}

// RecordCashMovement records a cash movement in the open shift. The shift's owner or a
// manager can put money in, only a manager can take money out.
func (s Shift) RecordCashMovement(by Cashier, kind CashMovementKind, amount money.Money, note string, now time.Time) (CashMovement, error) {
	if !s.Open() {
		return CashMovement{}, ErrNoShift
	}
	switch kind {
	case CashIn:
		if by.ID != s.CashierID && by.Role != RoleManager {
			return CashMovement{}, ErrNotShiftOwner
		}
	case CashOut:
		if by.Role != RoleManager {
			return CashMovement{}, ErrCashOutNotManager
		}
	default:
		return CashMovement{}, ErrCashKind
	}
	if amount <= 0 {
		return CashMovement{}, ErrCashAmount
	}
	note = strings.TrimSpace(note)
	if note == "" || utf8.RuneCountInString(note) > 200 {
		return CashMovement{}, ErrCashNote
	}
	return CashMovement{ShiftID: s.ID, Kind: kind, Amount: amount, Note: note, By: by.ID, At: now}, nil
}

// CanViewReport says whether the cashier can see the shift's report: a manager sees
// every shift, a cashier only their own shift.
func (s Shift) CanViewReport(viewer Cashier) bool {
	return viewer.Role == RoleManager || viewer.ID == s.CashierID
}

// DrawerOpen is opening the drawer without a sale (e.g. changing small
// coins). It is recorded in the shift for loss prevention; its count shows
// in the report.
type DrawerOpen struct {
	ID      int64
	ShiftID int64
	// By is the manager who opens (gives authority); RequestedBy is the cashier in the session.
	By          string
	RequestedBy string
	Reason      string
	At          time.Time
}

// RecordDrawerOpen records the opening of the drawer without a sale in the open shift.
// The one opening must be a manager (with manager approval in a cashier session).
func (s Shift) RecordDrawerOpen(by Cashier, requestedBy, reason string, now time.Time) (DrawerOpen, error) {
	if !s.Open() {
		return DrawerOpen{}, ErrNoShift
	}
	if by.Role != RoleManager {
		return DrawerOpen{}, ErrDrawerNotManager
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 200 {
		return DrawerOpen{}, ErrDrawerReason
	}
	return DrawerOpen{ShiftID: s.ID, By: by.ID, RequestedBy: requestedBy, Reason: reason, At: now}, nil
}

// ShiftReport is the report of the closed shift.
type ShiftReport struct {
	Shift
	Totals
	// CashIn and CashOut are the totals of the cash put into and taken out of the register.
	CashIn  money.Money
	CashOut money.Money
	// DrawerOpens is the number of times the drawer was opened without a sale.
	DrawerOpens int64
	// ExpectedCash is the cash that should be in the drawer: opening cash +
	// cash collection (change deducted) + put in − taken out.
	ExpectedCash money.Money
	// Difference is counted − expected cash; minus is a cash shortage, plus a cash
	// surplus.
	Difference money.Money
}

// BuildShiftReport computes the closed shift's report from its sales and cash
// movements. The count is blind: the expected cash and the difference are computed only
// after closing, the cashier does not see them before counting.
func BuildShiftReport(s Shift, completed []CompletedSale, voided []VoidedSale, movements []CashMovement, opens []DrawerOpen) (ShiftReport, error) {
	if s.Open() {
		return ShiftReport{}, ErrShiftNotClosed
	}
	r := ShiftReport{Shift: s, Totals: Sum(completed, voided), DrawerOpens: int64(len(opens))}
	for _, m := range movements {
		switch m.Kind {
		case CashIn:
			r.CashIn += m.Amount
		case CashOut:
			r.CashOut += m.Amount
		}
	}
	r.ExpectedCash = s.OpeningCash + r.CashIn - r.CashOut
	for _, p := range r.Payments {
		if p.Method == Cash {
			r.ExpectedCash += p.Amount
		}
	}
	r.Difference = s.CountedCash - r.ExpectedCash
	return r, nil
}
