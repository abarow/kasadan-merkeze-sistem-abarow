// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"testing"
	"time"

	"abarow.com/pkg/money"
)

var istanbul = mustLoad("Europe/Istanbul")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// TestBuild checks the hand-computed figures of the day report (the back
// house's day report; headquarters' summary is the same calculation).
func TestBuild(t *testing.T) {
	bread := Line{ProductID: "p3", Name: "EKMEK", Qty: money.Units(2), UnitPrice: 1200, TaxRate: 100}
	tomato := Line{ProductID: "p2", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100} // 112,38 TL
	shampoo := Line{ProductID: "p5", Name: "ŞAMPUAN", Qty: money.Units(1), UnitPrice: 1500, TaxRate: 2000}
	sales := []Sale{
		{
			// Half an hour after midnight (local time 00:30).
			ID: "s1", RegisterID: "register-1", CashierID: "ayse", ReceiptNo: 7,
			Total: 2400 + 11238, TaxTotal: 24 + 111, Change: 362,
			PaidAt:   time.Date(2026, 10, 25, 21, 30, 0, 0, time.UTC),
			Lines:    []Line{bread, tomato},
			Payments: []Payment{{Method: Cash, Amount: 14000}},
			Taxes:    []Tax{{Rate: 100, Gross: 13638, Tax: 135}},
		},
		{
			ID: "s2", RegisterID: "register-2", CashierID: "mert", ReceiptNo: 3,
			Total: 1500 + 1200, TaxTotal: 250 + 12,
			PaidAt:   time.Date(2026, 10, 26, 11, 5, 0, 0, time.UTC), // 14:05
			Lines:    []Line{shampoo, {ProductID: "p3", Name: "EKMEK", Qty: money.Units(1), UnitPrice: 1200, TaxRate: 100}},
			Payments: []Payment{{Method: Cash, Amount: 1000}, {Method: Card, Amount: 1700}},
			Taxes:    []Tax{{Rate: 100, Gross: 1200, Tax: 12}, {Rate: 2000, Gross: 1500, Tax: 250}},
		},
		{
			ID: "s3", RegisterID: "register-1", ReceiptNo: 8, // sale with no cashier
			Total: 1500, TaxTotal: 250,
			PaidAt:   time.Date(2026, 10, 26, 11, 40, 0, 0, time.UTC), // 14:40
			Lines:    []Line{shampoo},
			Payments: []Payment{{Method: "meal_card", Amount: 1500}},
			Taxes:    []Tax{{Rate: 2000, Gross: 1500, Tax: 250}},
		},
	}
	voids := []Void{
		{SaleID: "v1", RegisterID: "register-2", CashierID: "mert", Total: 999, At: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)},
		{SaleID: "v2", RegisterID: "register-1", CashierID: "ayse", Total: 500, Refunded: true, At: time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)},
	}
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, istanbul)
	r := Build(Filter{Start: start, End: start.AddDate(0, 0, 1), Location: istanbul}, sales, voids)

	if len(r.Days) != 1 || r.Days[0] != (DayTotal{Date: "2026-10-26", Receipts: 3, Total: 13638 + 2700 + 1500}) || r.Receipts != 3 || r.Total != 13638+2700+1500 || r.TaxTotal != 135+262+250 ||
		r.Voided != 2 || r.VoidedTotal != 1499 {
		t.Errorf("toplamlar: %+v", r)
	}
	// Cash: taken 140.00 + 10.00, change 3.62 deducted.
	wantPay := []PaymentTotal{{Cash, 14000 + 1000 - 362}, {Card, 1700}, {MealCard, 1500}}
	if len(r.Payments) != 3 || r.Payments[0] != wantPay[0] || r.Payments[1] != wantPay[1] || r.Payments[2] != wantPay[2] {
		t.Errorf("payments %+v", r.Payments)
	}
	var paid money.Money
	for _, p := range r.Payments {
		paid += p.Amount
	}
	if paid != r.Total {
		t.Errorf("payments total %s, sales total %s", paid, r.Total)
	}
	if len(r.Taxes) != 2 || r.Taxes[0] != (Tax{Rate: 100, Gross: 13638 + 1200, Tax: 135 + 12}) || r.Taxes[1] != (Tax{Rate: 2000, Gross: 3000, Tax: 500}) {
		t.Errorf("KDV %+v", r.Taxes)
	}
	wantRegs := []GroupTotal{
		{ID: "register-1", Receipts: 2, Total: 13638 + 1500, Voided: 1, VoidedTotal: 500},
		{ID: "register-2", Receipts: 1, Total: 2700, Voided: 1, VoidedTotal: 999},
	}
	if len(r.Registers) != 2 || r.Registers[0] != wantRegs[0] || r.Registers[1] != wantRegs[1] {
		t.Errorf("kasalar %+v", r.Registers)
	}
	wantCashiers := []GroupTotal{
		{ID: "", Receipts: 1, Total: 1500},
		{ID: "ayse", Receipts: 1, Total: 13638, Voided: 1, VoidedTotal: 500},
		{ID: "mert", Receipts: 1, Total: 2700, Voided: 1, VoidedTotal: 999},
	}
	if len(r.Cashiers) != 3 || r.Cashiers[0] != wantCashiers[0] || r.Cashiers[1] != wantCashiers[1] || r.Cashiers[2] != wantCashiers[2] {
		t.Errorf("kasiyerler %+v", r.Cashiers)
	}
	if len(r.Hours) != 24 || r.Hours[0] != (HourTotal{0, 1, 13638}) || r.Hours[14] != (HourTotal{14, 2, 4200}) || r.Hours[13].Receipts != 0 {
		t.Errorf("saatler: 0=%+v 14=%+v", r.Hours[0], r.Hours[14])
	}
	wantProducts := []ProductTotal{
		{ProductID: "p2", Name: "Domates", Qty: 1250, Total: 11238},
		{ProductID: "p3", Name: "EKMEK", Qty: 3000, Total: 3600},
		{ProductID: "p5", Name: "ŞAMPUAN", Qty: 2000, Total: 3000},
	}
	if len(r.Products) != 3 {
		t.Fatalf("products %+v", r.Products)
	}
	var lines money.Money
	for i, p := range r.Products {
		if p != wantProducts[i] {
			t.Errorf("product %d: %+v, want %+v", i, p, wantProducts[i])
		}
		lines += p.Total
	}
	if lines != r.Total {
		t.Errorf("products total %s, sales total %s", lines, r.Total)
	}
}

func TestBuildEmpty(t *testing.T) {
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, istanbul)
	r := Build(Filter{Start: start, End: start.AddDate(0, 0, 2), Location: istanbul}, nil, nil)
	if r.Receipts != 0 || r.Total != 0 || len(r.Payments) != 0 || len(r.Registers) != 0 || len(r.Hours) != 24 ||
		len(r.Days) != 2 || r.Days[1] != (DayTotal{Date: "2026-10-27"}) {
		t.Errorf("empty day: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	ok := Sale{ID: "s", RegisterID: "register-1", ReceiptNo: 1, PaidAt: time.Now()}
	if ok.Validate() != nil {
		t.Error("a valid sale was rejected")
	}
	for _, s := range []Sale{{RegisterID: "k", ReceiptNo: 1, PaidAt: time.Now()}, {ID: "s", ReceiptNo: 1, PaidAt: time.Now()}, {ID: "s", RegisterID: "k", PaidAt: time.Now()}, {ID: "s", RegisterID: "k", ReceiptNo: 1}} {
		if s.Validate() == nil {
			t.Errorf("an incomplete sale was accepted: %+v", s)
		}
	}
	if (Void{SaleID: "v", RegisterID: "k", At: time.Now()}).Validate() != nil || (Void{SaleID: "v", At: time.Now()}).Validate() == nil {
		t.Error("void check")
	}
}
