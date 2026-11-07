// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the rules of the end-of-day (X/Z) reports.
package domain

import (
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// PaymentMethod is the payment method.
type PaymentMethod string

const (
	Cash     PaymentMethod = paymethod.Cash
	Card     PaymentMethod = paymethod.Card
	MealCard PaymentMethod = paymethod.MealCard // meal card or voucher
	// Other is an unrecognized method (coming from a newer sales service):
	// the amount is not lost, it shows in the "Diğer" line of the report.
	Other PaymentMethod = paymethod.Other
)

// TaxTotal is the VAT-included total and the VAT of the sales at a VAT rate.
type TaxTotal struct {
	Rate  money.Rate
	Gross money.Money
	Tax   money.Money
}

// PaymentTotal is the collection made with a payment method.
type PaymentTotal struct {
	Method PaymentMethod
	Amount money.Money
}

// CompletedSale is the information of a closed sale that goes into the report
// (sale.completed.v1).
type CompletedSale struct {
	SaleID    string
	CashierID string
	ReceiptNo int64
	Total     money.Money
	TaxTotal  money.Money
	Change    money.Money
	Taxes     []TaxTotal
	Payments  []PaymentTotal
	At        time.Time
}

// VoidedSale is a sale cancelled without its payment being taken (sale.voided.v1).
type VoidedSale struct {
	SaleID    string
	CashierID string
	Total     money.Money
	At        time.Time
}

// ErrUnknownPeriod is returned when there is no period with the requested Z number.
var ErrUnknownPeriod = errors.New("bu numarada Z raporu yok")

// Period is the period between two Z reports. In the open period ClosedAt is zero.
type Period struct {
	ZNo      int64
	OpenedAt time.Time
	ClosedAt time.Time
}

// DayReport is a period's report. The fields follow the layout of GİB's YN ÖKC
// Z report.
type DayReport struct {
	Period
	Totals
}

// Totals are the totals of a group of sales (a period or a shift).
type Totals struct {
	ReceiptCount   int64
	FirstReceiptNo int64
	LastReceiptNo  int64
	// Taxes are in ascending order of rate.
	Taxes    []TaxTotal
	Total    money.Money
	TaxTotal money.Money
	// Payments is always two lines, in the order cash and card.
	Payments    []PaymentTotal
	VoidedCount int64
	VoidedTotal money.Money
}

// BuildReport computes the report from the period's sales.
func BuildReport(p Period, completed []CompletedSale, voided []VoidedSale) DayReport {
	return DayReport{Period: p, Totals: Sum(completed, voided)}
}

// Sum computes the totals of the sales. The totals are from the sales ledger's
// calculation (pkg/ledger; the same as the store server's and headquarters' reports):
// VAT is the total of the amounts computed on the receipts (not recomputed);
// change is deducted from the cash collection, so the payment total equals the sales
// total. The report's layout (GİB's Z report) is here: payments are
// cash, card, meal card and, if any, other.
func Sum(completed []CompletedSale, voided []VoidedSale) Totals {
	sales := make([]ledger.Sale, 0, len(completed))
	var r Totals
	for _, s := range completed {
		if r.FirstReceiptNo == 0 || s.ReceiptNo < r.FirstReceiptNo {
			r.FirstReceiptNo = s.ReceiptNo
		}
		r.LastReceiptNo = max(r.LastReceiptNo, s.ReceiptNo)
		sale := ledger.Sale{ID: s.SaleID, CashierID: s.CashierID, ReceiptNo: s.ReceiptNo, Total: s.Total, TaxTotal: s.TaxTotal,
			Change: s.Change, PaidAt: s.At}
		for _, t := range s.Taxes {
			sale.Taxes = append(sale.Taxes, ledger.Tax{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
		}
		for _, p := range s.Payments {
			sale.Payments = append(sale.Payments, ledger.Payment{Method: string(p.Method), Amount: p.Amount})
		}
		sales = append(sales, sale)
	}
	voids := make([]ledger.Void, 0, len(voided))
	for _, v := range voided {
		voids = append(voids, ledger.Void{SaleID: v.SaleID, CashierID: v.CashierID, Total: v.Total, At: v.At})
	}
	rep := ledger.Build(ledger.Filter{}, sales, voids)

	r.ReceiptCount, r.Total, r.TaxTotal = rep.Receipts, rep.Total, rep.TaxTotal
	r.VoidedCount, r.VoidedTotal = rep.Voided, rep.VoidedTotal
	for _, t := range rep.Taxes {
		r.Taxes = append(r.Taxes, TaxTotal{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
	}
	var cash, card, meal, other money.Money
	for _, p := range rep.Payments {
		switch PaymentMethod(p.Method) {
		case Cash:
			cash = p.Amount
		case Card:
			card = p.Amount
		case MealCard:
			meal = p.Amount
		default:
			other += p.Amount
		}
	}
	r.Payments = []PaymentTotal{{Method: Cash, Amount: cash}, {Method: Card, Amount: card}, {Method: MealCard, Amount: meal}}
	if other != 0 {
		r.Payments = append(r.Payments, PaymentTotal{Method: Other, Amount: other})
	}
	return r
}

// UnfinishedSalesError is returned when the end of day or a shift closing is requested while
// there is a sale whose payment was taken but whose fiscal record (receipt) could not be made:
// the report would be incomplete and the cash in the drawer would look excessive.
type UnfinishedSalesError struct {
	Count int64
}

func (e UnfinishedSalesError) Error() string {
	return fmt.Sprintf("ödemesi alınmış ama fişi basılamamış %d satış var; önce kasada \"Fişi tamamla\" ile fişlerini basın", e.Count)
}

// EventsPendingError is returned when the sale events expected for the end of day or a shift
// closing are not processed in time. Since the report would be incomplete the operation is
// not done; it is retried once the message path recovers.
type EventsPendingError struct {
	// Pending is the number of events pending at the last check.
	Pending int64
	// Err is the reason if the pending events could not be counted.
	Err error
}

func (e EventsPendingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("satış olaylarının işlenip işlenmediği anlaşılamadı (%v); rapor eksik kalmasın diye işlem yapılmadı, tekrar deneyin", e.Err)
	}
	return fmt.Sprintf("%d satış olayı henüz işlenmedi; rapor eksik kalmasın diye işlem yapılmadı, mesaj yolunu (NATS) kontrol edip tekrar deneyin", e.Pending)
}

func (e EventsPendingError) Unwrap() error { return e.Err }
