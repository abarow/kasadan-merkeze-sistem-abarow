// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the sale (receipt) concepts and business rules.
// It has no dependency on the database, the network or frameworks.
package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// SaleID is the sale's unique id.
type SaleID string

// Status is the sale's status: open → charged → paid, or open → voided.
type Status string

const (
	StatusOpen Status = "open"
	// StatusCharged: the payment was taken, the fiscal record is awaited. If the fiscal record
	// fails the sale stays in this status and on retry it continues with the same
	// payment; the customer is not charged a second time.
	StatusCharged Status = "charged"
	StatusPaid    Status = "paid"
	StatusVoided  Status = "voided"
)

// PaymentMethod is the payment method.
type PaymentMethod string

const (
	PaymentCash     PaymentMethod = paymethod.Cash
	PaymentCard     PaymentMethod = paymethod.Card
	PaymentMealCard PaymentMethod = paymethod.MealCard // meal card or voucher
)

var (
	ErrNoLines             = errors.New("satışta en az bir satır olmalı")
	ErrInvalidLine         = errors.New("geçersiz satış satırı")
	ErrNotOpen             = errors.New("satış açık değil")
	ErrNotCharged          = errors.New("satışın ödemesi alınmamış")
	ErrInvalidPayment      = calc.ErrInvalidPayment
	ErrInsufficientPayment = calc.ErrInsufficientPayment
	ErrExcessNonCash       = calc.ErrExcessNonCash
	ErrInvalidReceiptNo    = errors.New("fiş numarası pozitif olmalı")
	ErrInvalidStatus       = errors.New("bilinmeyen satış durumu")
)

// Line is an item in the sale. Prices include VAT.
type Line struct {
	ProductID string      `json:"product_id"`
	Barcode   string      `json:"barcode"`
	Name      string      `json:"name"`
	Qty       money.Qty   `json:"qty_milli"`
	UnitPrice money.Money `json:"unit_price_kurus"`
	TaxRate   money.Rate  `json:"tax_rate_bp"`
}

// Total is the line's amount including VAT.
func (l Line) Total() money.Money { return calc.Line(l.UnitPrice, l.Qty) }

func (l Line) validate() error {
	switch {
	case l.ProductID == "":
		return fmt.Errorf("%w: ürün kimliği boş", ErrInvalidLine)
	case l.Name == "":
		return fmt.Errorf("%w: ürün adı boş", ErrInvalidLine)
	case l.Qty <= 0:
		return fmt.Errorf("%w: miktar pozitif olmalı", ErrInvalidLine)
	case l.UnitPrice < 0:
		return fmt.Errorf("%w: fiyat negatif olamaz", ErrInvalidLine)
	case l.TaxRate < 0:
		return fmt.Errorf("%w: KDV oranı negatif olamaz", ErrInvalidLine)
	}
	if err := calc.CheckLine(l.UnitPrice, l.Qty); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidLine, err)
	}
	return nil
}

// Payment is a payment made to the sale.
type Payment struct {
	Method PaymentMethod `json:"method"`
	Amount money.Money   `json:"amount_kurus"`
	// Ref is the payment's counterpart in the payment service (e.g. the card transaction's
	// reference number); it can be empty for cash.
	Ref string `json:"ref,omitempty"`
	// Collect says the fiscal device (the POS inside the ÖKC) takes the payment together
	// with the receipt (e.g. Beko); it does not go through the payment service.
	Collect bool `json:"collect,omitempty"`
}

// TaxLine is the VAT-included total and tax amount that fall on a VAT rate.
type TaxLine struct {
	Rate  money.Rate  `json:"rate_bp"`
	Gross money.Money `json:"gross_kurus"`
	Tax   money.Money `json:"tax_kurus"`
}

// Sale is a receipt. Its status changes only through its methods.
type Sale struct {
	id         SaleID
	registerID string
	cashierID  string
	status     Status
	lines      []Line
	payments   []Payment
	receiptNo  int64
	fiscalNo   string
	createdAt  time.Time
	paidAt     time.Time
	voidedAt   time.Time
	// refunded: after the sale's payment was taken, because the fiscal record could not be made,
	// the payment was refunded and it was cancelled.
	refunded bool
	events   []Event
}

// NewSale creates an open sale with the given lines. cashierID is the cashier making the sale
// (audit trail); empty for a sale without a cashier.
func NewSale(id SaleID, registerID, cashierID string, lines []Line, now time.Time) (*Sale, error) {
	if id == "" || registerID == "" {
		return nil, errors.New("satış ve kasa kimliği boş olamaz")
	}
	if len(lines) == 0 {
		return nil, ErrNoLines
	}
	if len(lines) > calc.MaxLines {
		return nil, fmt.Errorf("%w: en çok %d satır: %w", ErrInvalidLine, calc.MaxLines, calc.ErrTooLarge)
	}
	for i, l := range lines {
		if err := l.validate(); err != nil {
			return nil, fmt.Errorf("satır %d: %w", i+1, err)
		}
	}
	return &Sale{
		id:         id,
		registerID: registerID,
		cashierID:  cashierID,
		status:     StatusOpen,
		lines:      slices.Clone(lines),
		createdAt:  now,
	}, nil
}

func (s *Sale) ID() SaleID           { return s.id }
func (s *Sale) RegisterID() string   { return s.registerID }
func (s *Sale) CashierID() string    { return s.cashierID }
func (s *Sale) Status() Status       { return s.status }
func (s *Sale) Lines() []Line        { return slices.Clone(s.lines) }
func (s *Sale) Payments() []Payment  { return slices.Clone(s.payments) }
func (s *Sale) ReceiptNo() int64     { return s.receiptNo }
func (s *Sale) FiscalNo() string     { return s.fiscalNo }
func (s *Sale) CreatedAt() time.Time { return s.createdAt }
func (s *Sale) PaidAt() time.Time    { return s.paidAt }
func (s *Sale) VoidedAt() time.Time  { return s.voidedAt }

// Refunded says the sale was cancelled with its payment refunded.
func (s *Sale) Refunded() bool { return s.refunded }

// items is the input of the lines to the calculation engine (pkg/calc).
func (s *Sale) items() []calc.Item {
	out := make([]calc.Item, 0, len(s.lines))
	for _, l := range s.lines {
		out = append(out, calc.Item{Total: l.Total(), Rate: l.TaxRate})
	}
	return out
}

// Total is the sale's total including VAT.
func (s *Sale) Total() money.Money { return calc.Sum(s.items()) }

// TaxBreakdown computes VAT by rate, over each rate's VAT-included total
// (the rule is in pkg/calc: the YN ÖKC receipt and the Z report also sum VAT by rate;
// the one-kuruş differences of summing line by line do not arise). The result is in
// ascending order of rate.
func (s *Sale) TaxBreakdown() []TaxLine {
	var out []TaxLine
	for _, t := range calc.Taxes(s.items()) {
		out = append(out, TaxLine{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
	}
	return out
}

// TaxTotal is the total VAT in the sale (TOPKDV on the receipt).
func (s *Sale) TaxTotal() money.Money {
	var total money.Money
	for _, t := range s.TaxBreakdown() {
		total += t.Tax
	}
	return total
}

// Paid is the total of the payments taken.
func (s *Sale) Paid() money.Money {
	var total money.Money
	for _, p := range s.payments {
		total += p.Amount
	}
	return total
}

// Change is the change to be given to the customer; zero in a sale whose payment has not been taken.
func (s *Sale) Change() money.Money {
	if s.status != StatusCharged && s.status != StatusPaid {
		return 0
	}
	return s.Paid() - s.Total()
}

// CheckPayments checks whether the payments can close the sale.
// Overpayment can only be in cash; the excess is given as change.
// It is called before the card is charged; RecordCharge repeats the same check.
func (s *Sale) CheckPayments(payments []Payment) error {
	if s.status != StatusOpen {
		return ErrNotOpen
	}
	in := make([]calc.Payment, 0, len(payments))
	for _, p := range payments {
		in = append(in, calc.Payment{Method: string(p.Method), Amount: p.Amount})
	}
	return calc.CheckPayments(s.Total(), in)
}

// PreviewCharge returns an unsaved copy of the sale with the payments processed. In a sale
// whose payment the fiscal device will take it is used for the receipt that goes to the device:
// the sale does not change until the payment is actually taken.
func (s *Sale) PreviewCharge(payments []Payment) (*Sale, error) {
	c := Restore(s.Snapshot())
	if err := c.RecordCharge(payments); err != nil {
		return nil, err
	}
	return c, nil
}

// RecordCharge records the payments taken on the sale and moves the sale to the
// awaiting-fiscal-record (charged) state.
func (s *Sale) RecordCharge(payments []Payment) error {
	if err := s.CheckPayments(payments); err != nil {
		return err
	}
	s.status = StatusCharged
	s.payments = slices.Clone(payments)
	return nil
}

// RecordFiscalFailure records the SaleCharged event for a sale whose fiscal record (receipt)
// could not be made and whose payment was taken (it stays charged); the status does not change.
func (s *Sale) RecordFiscalFailure(reason string, now time.Time) error {
	if s.status != StatusCharged {
		return ErrNotCharged
	}
	s.events = append(s.events, SaleCharged{
		SaleID:     s.id,
		RegisterID: s.registerID,
		CashierID:  s.cashierID,
		Total:      s.Total(),
		Lines:      slices.Clone(s.lines),
		Payments:   slices.Clone(s.payments),
		Reason:     reason,
		At:         now,
	})
	return nil
}

// MarkPaid closes the sale whose payment was taken and fiscal record made and records the
// SaleCompleted event.
func (s *Sale) MarkPaid(receiptNo int64, fiscalNo string, now time.Time) error {
	if s.status != StatusCharged {
		return ErrNotCharged
	}
	if receiptNo <= 0 {
		return ErrInvalidReceiptNo
	}
	s.status = StatusPaid
	s.receiptNo = receiptNo
	s.fiscalNo = fiscalNo
	s.paidAt = now
	s.events = append(s.events, SaleCompleted{
		SaleID:     s.id,
		RegisterID: s.registerID,
		CashierID:  s.cashierID,
		ReceiptNo:  receiptNo,
		FiscalNo:   fiscalNo,
		Total:      s.Total(),
		TaxTotal:   s.TaxTotal(),
		Change:     s.Change(),
		Taxes:      s.TaxBreakdown(),
		Lines:      slices.Clone(s.lines),
		Payments:   slices.Clone(s.payments),
		At:         now,
	})
	return nil
}

// Void cancels a sale whose payment has not been taken and records the SaleVoided event.
// Reversing a sale whose payment was taken is a separate refund flow.
// VoidRefunded cancels a sale whose payment was taken but whose fiscal record (receipt)
// could not be made, after its payment has been given back to the customer. Since no receipt
// was ever issued no fiscal document is needed; the sale is reported as if it was cancelled
// without its payment being taken. Refunding the payment (reversing the card transaction,
// giving the cash) is the caller's responsibility.
func (s *Sale) VoidRefunded(now time.Time) error {
	if s.status != StatusCharged {
		return ErrNotCharged
	}
	s.status = StatusVoided
	s.voidedAt = now
	s.refunded = true
	s.events = append(s.events, SaleVoided{
		SaleID:     s.id,
		RegisterID: s.registerID,
		CashierID:  s.cashierID,
		Total:      s.Total(),
		Refunded:   true,
		At:         now,
	})
	return nil
}

func (s *Sale) Void(now time.Time) error {
	if s.status != StatusOpen {
		return ErrNotOpen
	}
	s.status = StatusVoided
	s.voidedAt = now
	s.events = append(s.events, SaleVoided{
		SaleID:     s.id,
		RegisterID: s.registerID,
		CashierID:  s.cashierID,
		Total:      s.Total(),
		At:         now,
	})
	return nil
}

// PullEvents returns the recorded events and empties the list.
func (s *Sale) PullEvents() []Event {
	events := s.events
	s.events = nil
	return events
}

// Snapshot is the state of the sale to be made permanent. Only the repository
// adapters use it.
type Snapshot struct {
	ID         SaleID
	RegisterID string
	CashierID  string
	Status     Status
	Lines      []Line
	Payments   []Payment
	ReceiptNo  int64
	FiscalNo   string
	CreatedAt  time.Time
	PaidAt     time.Time
	VoidedAt   time.Time
	Refunded   bool
}

// Snapshot returns the sale's permanent state.
func (s *Sale) Snapshot() Snapshot {
	return Snapshot{
		ID:         s.id,
		RegisterID: s.registerID,
		CashierID:  s.cashierID,
		Status:     s.status,
		Lines:      slices.Clone(s.lines),
		Payments:   slices.Clone(s.payments),
		ReceiptNo:  s.receiptNo,
		FiscalNo:   s.fiscalNo,
		CreatedAt:  s.createdAt,
		PaidAt:     s.paidAt,
		VoidedAt:   s.voidedAt,
		Refunded:   s.refunded,
	}
}

// Restore rebuilds the sale from a saved state.
func Restore(snap Snapshot) *Sale {
	return &Sale{
		id:         snap.ID,
		registerID: snap.RegisterID,
		cashierID:  snap.CashierID,
		status:     snap.Status,
		lines:      slices.Clone(snap.Lines),
		payments:   slices.Clone(snap.Payments),
		receiptNo:  snap.ReceiptNo,
		fiscalNo:   snap.FiscalNo,
		createdAt:  snap.CreatedAt,
		paidAt:     snap.PaidAt,
		voidedAt:   snap.VoidedAt,
		refunded:   snap.Refunded,
	}
}
