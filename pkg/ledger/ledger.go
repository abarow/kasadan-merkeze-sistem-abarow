// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package ledger is the sales ledger: the single model and the single decoder of the sale and
// shift events coming from the register. The store server (report) and headquarters
// (ingest, reporting) use the same types; every record has scope fields
// (StoreID, RegisterID), which are filled in as the tier goes up.
//
// Amounts are in kuruş (money.Money), quantity in thousandths (money.Qty), the VAT rate in
// base points (money.Rate).
package ledger

import (
	"errors"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// ErrInvalidEvent is an event too incomplete to be written to the ledger.
var ErrInvalidEvent = errors.New("olay eksik")

// The short names of the payment types in the sales contract; an unknown type comes with its
// own name.
const (
	Cash     = paymethod.Cash
	Card     = paymethod.Card
	MealCard = paymethod.MealCard
)

// Sale is a completed sale (sale.completed.v1): its payment was taken and its fiscal
// record (receipt) made.
type Sale struct {
	ID, StoreID, RegisterID, CashierID string
	// ReceiptNo is the receipt number that increases per register; a gap is a missing receipt.
	ReceiptNo int64
	FiscalNo  string
	// Total is the VAT-included total; TaxTotal is the VAT in it.
	Total, TaxTotal money.Money
	// Change is the change given from cash.
	Change   money.Money
	PaidAt   time.Time
	Lines    []Line
	Payments []Payment
	Taxes    []Tax
}

// Line is a sale line.
type Line struct {
	ProductID, Barcode, Name string
	Qty                      money.Qty
	UnitPrice                money.Money
	TaxRate                  money.Rate
}

// Total is the line's VAT-included amount; the same as the calculation at the register.
func (l Line) Total() money.Money { return calc.Line(l.UnitPrice, l.Qty) }

// Payment is one of the sale's payments. The money taken in cash is written; the change
// stays separately on the sale.
type Payment struct {
	Method string
	Amount money.Money
}

// Tax is the sale's total at a VAT rate (as computed at the register).
type Tax struct {
	Rate       money.Rate
	Gross, Tax money.Money
}

// Void is a cancelled sale (sale.voided.v1): a sale cancelled before its payment was taken or
// one whose payment was refunded because its fiscal record could not be made
// (Refunded). It did not get a receipt number.
type Void struct {
	SaleID, StoreID, RegisterID, CashierID string
	Total                                  money.Money
	Refunded                               bool
	At                                     time.Time
}

// Unfinished is a sale whose payment was taken but whose fiscal record (receipt) could not be made
// (sale.charged.v1). The register reports it again at every failed attempt; it stops
// waiting when the sale is completed or its payment is refunded and it is cancelled.
type Unfinished struct {
	SaleID, StoreID, RegisterID, CashierID string
	Total                                  money.Money
	// Reason is why the fiscal record could not be made in the attempt.
	Reason string
	// At is the time of the attempt.
	At time.Time
}

// Shift is a shift at the register (shift.opened.v1, shift.closed.v1).
// The shift number increases within the register; a shift is identified by the register id
// and the number.
type Shift struct {
	StoreID, RegisterID    string
	ID                     int64
	CashierID, CashierName string
	OpenedAt               time.Time
	OpeningCash            money.Money
	// Closing details; ClosedAt is zero in an open shift.
	ClosedAt time.Time
	ClosedBy string
	Receipts int64
	Total    money.Money
	// ExpectedCash: opening cash + cash collection + put into the register − taken out of
	// the register. Difference = CountedCash − ExpectedCash: minus is a cash shortage,
	// plus a cash surplus.
	ExpectedCash, CountedCash, Difference money.Money
}

// Open says the shift is still open.
func (s Shift) Open() bool { return s.ClosedAt.IsZero() }

// Validate checks that the sale can be written to the ledger. The store id is not
// looked for here: at the store server it may be empty in the register's event, headquarters
// fills it with the store the event came from.
func (s Sale) Validate() error {
	if s.ID == "" || s.RegisterID == "" || s.ReceiptNo <= 0 || s.PaidAt.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

// Validate checks that the cancellation can be written to the ledger.
func (v Void) Validate() error {
	if v.SaleID == "" || v.RegisterID == "" || v.At.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

// Validate checks that the sale whose receipt could not be issued can be written to the ledger.
func (u Unfinished) Validate() error {
	if u.SaleID == "" || u.RegisterID == "" || u.At.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

// Validate checks that the shift can be written to the ledger.
func (s Shift) Validate() error {
	if s.RegisterID == "" || s.ID <= 0 || s.OpenedAt.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}
