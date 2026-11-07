// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package calc

import (
	"errors"
	"testing"

	"abarow.com/pkg/money"
)

func TestLineAndSum(t *testing.T) {
	// 1.250 kg × 89.90 TL = 112.375 → 112.38 TL (half a kuruş rounded up).
	if got := Line(8990, 1250); got != 11238 {
		t.Errorf("Line = %d", got)
	}
	if got := Sum([]Item{{Total: 100}, {Total: 250}}); got != 350 {
		t.Errorf("Sum = %d", got)
	}
}

func TestTaxes(t *testing.T) {
	items := []Item{{Total: 1200, Rate: 2000}, {Total: 1000, Rate: 1000}, {Total: 600, Rate: 2000}}
	got := Taxes(items)
	// By rate, in ascending order; VAT over each rate's total (1800 → 300).
	want := []Tax{{Rate: 1000, Gross: 1000, Tax: 91}, {Rate: 2000, Gross: 1800, Tax: 300}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Taxes = %+v, istenen %+v", got, want)
	}
	if TaxTotal(got) != 391 {
		t.Errorf("TaxTotal = %d", TaxTotal(got))
	}
	if len(Taxes(nil)) != 0 {
		t.Error("the VAT breakdown of a receipt with no lines should be empty")
	}
}

func TestCheckPayments(t *testing.T) {
	cash := func(a money.Money) Payment { return Payment{Method: "cash", Amount: a} }
	card := func(a money.Money) Payment { return Payment{Method: "card", Amount: a} }
	meal := func(a money.Money) Payment { return Payment{Method: "meal_card", Amount: a} }
	for _, tt := range []struct {
		name     string
		payments []Payment
		want     error
	}{
		{"tam nakit", []Payment{cash(12500)}, nil},
		{"fazla nakit (para üstü)", []Payment{cash(20000)}, nil},
		{"kart + nakit, fazlası nakitte", []Payment{card(10000), cash(5000)}, nil},
		{"fazla kart", []Payment{card(13000)}, ErrExcessNonCash},
		{"kart + nakit, fazlası nakitten çok", []Payment{card(13000), cash(500)}, ErrExcessNonCash},
		{"fazla yemek kartı", []Payment{meal(13000)}, ErrExcessNonCash},
		{"eksik", []Payment{cash(12000)}, ErrInsufficientPayment},
		{"ödemesiz", nil, ErrInsufficientPayment},
		{"sıfır tutar", []Payment{cash(0), cash(12500)}, ErrInvalidPayment},
		{"negatif tutar", []Payment{cash(-100), cash(12600)}, ErrInvalidPayment},
		{"bilinmeyen yöntem", []Payment{{Method: "çek", Amount: 12500}}, ErrInvalidPayment},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckPayments(12500, tt.payments); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if got := Change(12500, []Payment{card(10000), cash(5000)}); got != 2500 {
		t.Errorf("Change = %d", got)
	}
}

func TestReceiptValidate(t *testing.T) {
	ok := Receipt{
		Items:    []Item{{Total: 1200, Rate: 1000}, {Total: 300, Rate: 2000}},
		Taxes:    []Tax{{Rate: 1000, Gross: 1200, Tax: 109}, {Rate: 2000, Gross: 300, Tax: 50}},
		Payments: []Payment{{Method: "cash", Amount: 2000}},
		Total:    1500,
		Change:   500,
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("consistent receipt: %v", err)
	}
	noTaxes := ok
	noTaxes.Taxes = nil
	if err := noTaxes.Validate(); err != nil {
		t.Errorf("receipt with no VAT breakdown: %v", err)
	}
	for name, mutate := range map[string]func(*Receipt){
		"kalem yok":          func(r *Receipt) { r.Items = nil },
		"toplam tutmuyor":    func(r *Receipt) { r.Total = 1600 },
		"KDV tutmuyor":       func(r *Receipt) { r.Taxes[0].Gross = 1100 },
		"para üstü tutmuyor": func(r *Receipt) { r.Change = 400 },
	} {
		r := ok
		r.Items = append([]Item(nil), ok.Items...)
		r.Taxes = append([]Tax(nil), ok.Taxes...)
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInconsistent) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
