// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// ErrNotProtobuf is an event that is not protobuf. The sales and staff events
// have been published as protobuf since v0.23; older JSON events are not taken into the
// ledger.
var ErrNotProtobuf = errors.New("the event is not protobuf (an old JSON event)")

// Subjects are the event subjects the ledger decodes.
var Subjects = []string{"sale.completed.v1", "sale.voided.v1", "sale.charged.v1", "shift.opened.v1", "shift.closed.v1"}

// Decode turns the event into the type to be written to the ledger: Sale, Void, Unfinished or
// Shift. It returns nil for a subject the ledger does not know.
func Decode(subject, contentType string, data []byte) (any, error) {
	switch subject {
	case "sale.completed.v1", "sale.voided.v1", "sale.charged.v1", "shift.opened.v1", "shift.closed.v1":
	default:
		return nil, nil
	}
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return nil, ErrNotProtobuf
	}
	switch subject {
	case "sale.completed.v1":
		return decodeCompleted(data)
	case "sale.voided.v1":
		return decodeVoided(data)
	case "sale.charged.v1":
		return decodeCharged(data)
	default:
		return decodeShift(subject, data)
	}
}

func decodeCompleted(data []byte) (Sale, error) {
	var e salesv1.SaleCompleted
	if err := proto.Unmarshal(data, &e); err != nil {
		return Sale{}, fmt.Errorf("could not decode sale.completed.v1: %w", err)
	}
	sale := e.GetSale()
	s := Sale{
		ID:         sale.GetId(),
		StoreID:    e.GetStoreId(),
		RegisterID: e.GetRegisterId(),
		CashierID:  sale.GetCashierId(),
		ReceiptNo:  sale.GetReceiptNo(),
		FiscalNo:   sale.GetFiscalNo(),
		Total:      money.Money(sale.GetTotalKurus()),
		TaxTotal:   money.Money(sale.GetTaxTotalKurus()),
		Change:     money.Money(sale.GetChangeKurus()),
	}
	if s.RegisterID == "" {
		s.RegisterID = sale.GetRegisterId()
	}
	if sale.GetPaidAt() != nil {
		s.PaidAt = sale.GetPaidAt().AsTime()
	} else if e.GetOccurredAt() != nil {
		s.PaidAt = e.GetOccurredAt().AsTime()
	}
	for _, l := range sale.GetLines() {
		s.Lines = append(s.Lines, Line{
			ProductID: l.GetProductId(), Barcode: l.GetBarcode(), Name: l.GetName(),
			Qty: money.Qty(l.GetQtyMilli()), UnitPrice: money.Money(l.GetUnitPriceKurus()), TaxRate: money.Rate(l.GetTaxRateBp()),
		})
	}
	for _, p := range sale.GetPayments() {
		s.Payments = append(s.Payments, Payment{Method: method(p.GetMethod()), Amount: money.Money(p.GetAmountKurus())})
	}
	for _, t := range sale.GetTaxes() {
		s.Taxes = append(s.Taxes, Tax{Rate: money.Rate(t.GetRateBp()), Gross: money.Money(t.GetGrossKurus()), Tax: money.Money(t.GetTaxKurus())})
	}
	return s, nil
}

func decodeVoided(data []byte) (Void, error) {
	var e salesv1.SaleVoided
	if err := proto.Unmarshal(data, &e); err != nil {
		return Void{}, fmt.Errorf("could not decode sale.voided.v1: %w", err)
	}
	v := Void{
		SaleID: e.GetSaleId(), StoreID: e.GetStoreId(), RegisterID: e.GetRegisterId(), CashierID: e.GetCashierId(),
		Total: money.Money(e.GetTotalKurus()), Refunded: e.GetRefunded(),
	}
	if e.GetOccurredAt() != nil {
		v.At = e.GetOccurredAt().AsTime()
	}
	return v, nil
}

func decodeCharged(data []byte) (Unfinished, error) {
	var e salesv1.SaleCharged
	if err := proto.Unmarshal(data, &e); err != nil {
		return Unfinished{}, fmt.Errorf("could not decode sale.charged.v1: %w", err)
	}
	sale := e.GetSale()
	u := Unfinished{
		SaleID: sale.GetId(), StoreID: e.GetStoreId(), RegisterID: e.GetRegisterId(), CashierID: sale.GetCashierId(),
		Total: money.Money(sale.GetTotalKurus()), Reason: e.GetReason(),
	}
	if u.RegisterID == "" {
		u.RegisterID = sale.GetRegisterId()
	}
	if e.GetOccurredAt() != nil {
		u.At = e.GetOccurredAt().AsTime()
	}
	return u, nil
}

func decodeShift(subject string, data []byte) (Shift, error) {
	var storeID, registerID string
	var s *staffv1.Shift
	var r *staffv1.ShiftReport
	switch subject {
	case "shift.opened.v1":
		var e staffv1.ShiftOpened
		if err := proto.Unmarshal(data, &e); err != nil {
			return Shift{}, fmt.Errorf("could not decode %s: %w", subject, err)
		}
		storeID, registerID, s = e.GetStoreId(), e.GetRegisterId(), e.GetShift()
	default:
		var e staffv1.ShiftClosed
		if err := proto.Unmarshal(data, &e); err != nil {
			return Shift{}, fmt.Errorf("could not decode %s: %w", subject, err)
		}
		storeID, registerID, r = e.GetStoreId(), e.GetRegisterId(), e.GetReport()
		s = r.GetShift()
	}
	sh := Shift{
		StoreID: storeID, RegisterID: registerID, ID: s.GetId(), CashierID: s.GetCashierId(), CashierName: s.GetCashierName(),
		OpeningCash: money.Money(s.GetOpeningCashKurus()), ClosedBy: s.GetClosedBy(),
	}
	if s.GetOpenedAt() != nil {
		sh.OpenedAt = s.GetOpenedAt().AsTime()
	}
	if r != nil {
		if s.GetClosedAt() == nil {
			return Shift{}, fmt.Errorf("%s: no closing time: %w", subject, ErrInvalidEvent)
		}
		sh.ClosedAt = s.GetClosedAt().AsTime()
		sh.Receipts, sh.Total = r.GetReceiptCount(), money.Money(r.GetTotalKurus())
		sh.ExpectedCash, sh.CountedCash = money.Money(r.GetExpectedCashKurus()), money.Money(s.GetCountedCashKurus())
		sh.Difference = money.Money(r.GetDifferenceKurus())
	}
	return sh, nil
}

// method turns the payment type into its short name in the sales contract (cash,
// card, meal_card); an unknown type stays with its own name.
func method(m salesv1.PaymentMethod) string {
	if name := paymethod.Name(int32(m)); name != "" {
		return name
	}
	return strings.ToLower(strings.TrimPrefix(m.String(), "PAYMENT_METHOD_"))
}
