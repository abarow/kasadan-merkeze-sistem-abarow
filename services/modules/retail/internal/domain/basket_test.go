// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
)

var (
	bread  = Product{ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: Piece, UnitPrice: 1500, TaxRate: 100}
	tomato = Product{ID: "domates", Name: "Domates", Barcode: "2700001", Unit: Kg, UnitPrice: 4990, TaxRate: 100}
)

func TestParseScaleBarcode(t *testing.T) {
	tests := []struct {
		code    string
		key     string
		qty     money.Qty
		ok      bool
		wantErr bool
	}{
		{"2700001012504", "2700001", 1250, true, false}, // 1,250 kg
		{"2700002008001", "2700002", 800, true, false},  // 0,800 kg
		{"2700001012505", "", 0, true, true},            // the check digit is wrong
		{"2700001000006", "", 0, true, true},            // the weight is zero
		{"8690000000012", "", 0, false, false},          // a normal product barcode
		{"2700001", "", 0, false, false},                // a catalog key, not a scale barcode
	}
	for _, tt := range tests {
		key, qty, ok, err := ParseScaleBarcode(tt.code)
		if key != tt.key || qty != tt.qty || ok != tt.ok || (err != nil) != tt.wantErr {
			t.Errorf("ParseScaleBarcode(%s) = %q, %d, %v, %v", tt.code, key, qty, ok, err)
		}
	}
}

func TestCheckDigit(t *testing.T) {
	for _, code := range []string{"8690000000012", "8690000000104", "2700001012504", "4006381333931"} {
		if got := CheckDigit(code[:12]); got != code[12] {
			t.Errorf("CheckDigit(%s) = %c, istenen %c", code[:12], got, code[12])
		}
	}
}

func TestAddMergesPiecesButNotWeighedLines(t *testing.T) {
	b := NewBasket("b1", "ayse", time.Now())
	steps := []struct {
		p       Product
		qty     money.Qty
		weighed bool
	}{
		{bread, money.Units(1), false},
		{bread, money.Units(1), false},
		{tomato, 1250, true},
		{tomato, 800, true},
	}
	for _, s := range steps {
		if err := b.Add(s.p, s.qty, s.weighed); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.Lines) != 3 || b.Lines[0].Qty != 2000 || b.Lines[1].Qty != 1250 || b.Lines[2].Qty != 800 {
		t.Fatalf("lines are wrong: %+v", b.Lines)
	}
	// 2 × 15,00 + 49,90 × 1,250 (62,375 → 62,38) + 49,90 × 0,800 (39,92)
	if got := b.Total(); got != 3000+6238+3992 {
		t.Errorf("Total = %d, istenen %d", got, 3000+6238+3992)
	}
}

func TestBasketRules(t *testing.T) {
	b := NewBasket("b1", "ayse", time.Now())
	if err := b.Add(tomato, money.Units(1), false); !errors.Is(err, ErrNeedsWeight) {
		t.Errorf("tomatoes without a scale: err = %v, want %v", err, ErrNeedsWeight)
	}
	if err := b.CheckOut("s1"); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty basket: err = %v, want %v", err, ErrEmpty)
	}
	_ = b.Add(bread, money.Units(1), false)
	_ = b.Add(tomato, 1250, true)
	if err := b.SetQty(0, money.Units(3)); err != nil || b.Lines[0].Qty != 3000 {
		t.Errorf("SetQty: %v, %+v", err, b.Lines[0])
	}
	if err := b.SetQty(1, 2000); !errors.Is(err, ErrWeighedQty) {
		t.Errorf("SetQty on a weighed line: err = %v, want %v", err, ErrWeighedQty)
	}
	if err := b.SetQty(0, 0); !errors.Is(err, ErrInvalidQty) {
		t.Errorf("zero quantity: err = %v, want %v", err, ErrInvalidQty)
	}
	if err := b.Remove(5); !errors.Is(err, ErrNoLine) {
		t.Errorf("non-existent line: err = %v, want %v", err, ErrNoLine)
	}
	if err := b.Remove(1); err != nil || len(b.Lines) != 1 {
		t.Errorf("Remove: %v, %+v", err, b.Lines)
	}
	if err := b.CheckOut("s1"); err != nil || b.Status != CheckedOut || b.SaleID != "s1" {
		t.Fatalf("CheckOut: %v, %+v", err, b)
	}
	if err := b.Add(bread, money.Units(1), false); !errors.Is(err, ErrClosed) {
		t.Errorf("adding to a closed basket: err = %v, want %v", err, ErrClosed)
	}
	b.Reopen()
	if b.Status != Open || b.SaleID != "" {
		t.Errorf("Reopen: %+v", b)
	}
}

// A quantity above the limit is rejected, also when it is reached by scanning again.
func TestQuantityLimit(t *testing.T) {
	b := NewBasket("b1", "ayse", time.Now())
	if err := b.Add(bread, calc.MaxQty+1, false); !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("quantity above the limit: %v", err)
	}
	if err := b.Add(bread, calc.MaxQty, false); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(bread, money.QtyScale, false); !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("a second scan crossing the limit: %v", err)
	}
	if err := b.SetQty(0, calc.MaxQty+1); !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("setting a quantity above the limit: %v", err)
	}
}
