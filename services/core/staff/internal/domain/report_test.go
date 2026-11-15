// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildReport(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	p := Period{ZNo: 7, OpenedAt: at}
	completed := []CompletedSale{
		{ // cash 200.00, change 74.63
			SaleID: "s1", ReceiptNo: 12, Total: 12537, TaxTotal: 328, Change: 7463,
			Taxes:    []TaxTotal{{Rate: 100, Gross: 11238, Tax: 111}, {Rate: 2000, Gross: 1299, Tax: 217}},
			Payments: []PaymentTotal{{Method: Cash, Amount: 20000}},
		},
		{ // GİB example: cash 29.00 + card 20.00
			SaleID: "s2", ReceiptNo: 11, Total: 4900, TaxTotal: 462,
			Taxes:    []TaxTotal{{Rate: 100, Gross: 1200, Tax: 12}, {Rate: 1000, Gross: 2200, Tax: 200}, {Rate: 2000, Gross: 1500, Tax: 250}},
			Payments: []PaymentTotal{{Method: Cash, Amount: 2900}, {Method: Card, Amount: 2000}},
		},
	}
	voided := []VoidedSale{{SaleID: "s3", Total: 1299}, {SaleID: "s4", Total: 500}}

	got := BuildReport(p, completed, voided)
	want := DayReport{Period: p, Totals: Totals{
		ReceiptCount:   2,
		FirstReceiptNo: 11,
		LastReceiptNo:  12,
		Taxes: []TaxTotal{
			{Rate: 100, Gross: 12438, Tax: 123},
			{Rate: 1000, Gross: 2200, Tax: 200},
			{Rate: 2000, Gross: 2799, Tax: 467},
		},
		Total:    17437,
		TaxTotal: 790,
		// Cash: 200.00 + 29.00 − 74.63 change = 154.37; card 20.00.
		Payments:    []PaymentTotal{{Method: Cash, Amount: 15437}, {Method: Card, Amount: 2000}, {Method: MealCard, Amount: 0}},
		VoidedCount: 2,
		VoidedTotal: 1799,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("report is wrong:\n%+v\nwant\n%+v", got, want)
	}
	if got.Payments[0].Amount+got.Payments[1].Amount != got.Total {
		t.Error("payment total does not equal sales total")
	}
}

// Meal card is a separate line; the amount of an unrecognized method is not lost, it goes
// to the "Diğer" line: the payment total stays equal to the sales total.
func TestBuildReportMealCardAndOther(t *testing.T) {
	completed := []CompletedSale{
		{SaleID: "s1", ReceiptNo: 1, Total: 5000, Payments: []PaymentTotal{{Method: MealCard, Amount: 4000}, {Method: Cash, Amount: 1000}}},
		{SaleID: "s2", ReceiptNo: 2, Total: 700, Payments: []PaymentTotal{{Method: "puan", Amount: 700}}},
	}
	got := BuildReport(Period{ZNo: 3}, completed, nil)
	want := []PaymentTotal{{Method: Cash, Amount: 1000}, {Method: Card, Amount: 0}, {Method: MealCard, Amount: 4000}, {Method: Other, Amount: 700}}
	if !reflect.DeepEqual(got.Payments, want) {
		t.Errorf("payments %+v, want %+v", got.Payments, want)
	}
	lines := RenderDayReport(got, 32, time.UTC, time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC))
	text := strings.Join(lines, "\n")
	for _, s := range []string{"Yemek kartı", "Diğer"} {
		if !strings.Contains(text, s) {
			t.Errorf("no %q in the printed report:\n%s", s, text)
		}
	}
}

func TestBuildReportEmpty(t *testing.T) {
	got := BuildReport(Period{ZNo: 1}, nil, nil)
	if got.ReceiptCount != 0 || got.Total != 0 || len(got.Payments) != 3 || got.FirstReceiptNo != 0 {
		t.Errorf("empty report is wrong: %+v", got)
	}
}
