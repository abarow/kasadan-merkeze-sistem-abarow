// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package money

import (
	"math"
	"testing"
)

func TestDivRound(t *testing.T) {
	tests := []struct {
		num, den, want int64
	}{
		{0, 7, 0},
		{10, 4, 3},   // 2,5 → 3
		{-10, 4, -3}, // -2,5 → -3
		{9, 4, 2},    // 2,25 → 2
		{-9, 4, -2},  // -2,25 → -2
		{11, 4, 3},   // 2,75 → 3
		{-11, 4, -3}, // -2,75 → -3
		{7, 2, 4},    // 3,5 → 4
		{5, 3, 2},    // 1,67 → 2
		{4, 3, 1},    // 1,33 → 1
		{-5, 3, -2},  // -1,67 → -2
		{math.MaxInt64, 1, math.MaxInt64},
		{math.MinInt64, 1, math.MinInt64},
		{math.MinInt64, 2, math.MinInt64 / 2},
	}
	for _, tt := range tests {
		if got := divRound(tt.num, tt.den); got != tt.want {
			t.Errorf("divRound(%d, %d) = %d, want %d", tt.num, tt.den, got, tt.want)
		}
	}
}

func TestMulDivRoundLargeIntermediate(t *testing.T) {
	// a×b does not fit in int64 but the result does.
	if got := mulDivRound(math.MaxInt64, 1000, 1000); got != math.MaxInt64 {
		t.Errorf("mulDivRound(MaxInt64, 1000, 1000) = %d, want %d", got, int64(math.MaxInt64))
	}
	if got := mulDivRound(math.MinInt64, 1000, 1000); got != math.MinInt64 {
		t.Errorf("mulDivRound(MinInt64, 1000, 1000) = %d, want %d", got, int64(math.MinInt64))
	}
}

func TestMulDivRoundPanics(t *testing.T) {
	tests := []struct {
		name      string
		a, b, den int64
	}{
		{"pozitif taşma", math.MaxInt64, 2, 1},
		{"128 bit taşma", math.MaxInt64, math.MaxInt64, 1},
		{"MinInt64 × -1", math.MinInt64, -1, 1},
		{"sıfır payda", 1, 1, 0},
		{"negatif payda", 1, 1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("mulDivRound(%d, %d, %d) panic etmedi", tt.a, tt.b, tt.den)
				}
			}()
			mulDivRound(tt.a, tt.b, tt.den)
		})
	}
}

func TestMulQty(t *testing.T) {
	tests := []struct {
		name  string
		price Money
		qty   Qty
		want  Money
	}{
		{"1 adet", 1299, Units(1), 1299},
		{"3 adet", 1299, Units(3), 3897},
		{"tartılı ürün", 8990, 1250, 11238},          // 89,90 × 1,250 kg = 112,375 → 112,38
		{"iade: negatif fiyat", -8990, 1250, -11238}, // -112,375 → -112,38
		{"iade: negatif miktar", 8990, -1250, -11238},
		{"küsuratlı miktar", 1999, 333, 666}, // 19,99 × 0,333 = 6,657 → 6,66
		{"sıfır fiyat", 0, Units(5), 0},
		{"tam yarım", 1, 500, 1}, // 0,005 → 0,01
		{"yarımın altı", 1, 499, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.price.MulQty(tt.qty); got != tt.want {
				t.Errorf("%d.MulQty(%d) = %d, want %d", tt.price, tt.qty, got, tt.want)
			}
		})
	}
}

func TestIncludedTax(t *testing.T) {
	tests := []struct {
		name  string
		gross Money
		rate  Rate
		want  Money
	}{
		{"%20 tam", 12000, 2000, 2000},  // 120,00 TL → 20,00 TL
		{"%20 yarım", 1299, 2000, 217},  // 216,5 → 217
		{"%20 iade", -1299, 2000, -217}, // -216,5 → -217
		{"%10", 1299, 1000, 118},        // 118,09 → 118
		{"%10 tam", 11000, 1000, 1000},  // 110,00 TL → 10,00 TL
		{"%1", 10100, 100, 100},         // 101,00 TL → 1,00 TL
		{"%0", 10000, 0, 0},             // product without VAT
		{"sıfır tutar", 0, 2000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.gross.IncludedTax(tt.rate); got != tt.want {
				t.Errorf("%d.IncludedTax(%d) = %d, want %d", tt.gross, tt.rate, got, tt.want)
			}
		})
	}
}

func TestIncludedTaxNegativeRatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a negative VAT rate did not panic")
		}
	}()
	Money(1000).IncludedTax(-1)
}

func TestMulRate(t *testing.T) {
	tests := []struct {
		amount Money
		rate   Rate
		want   Money
	}{
		{1555, 1000, 156},   // 15,55 × %10 = 1,555 → 1,56
		{-1555, 1000, -156}, // iade
		{10000, 2000, 2000}, // 100,00 × %20 = 20,00
		{1, 5000, 1},        // 0,005 → 0,01
		{1, 4999, 0},
		{12345, 0, 0},
	}
	for _, tt := range tests {
		if got := tt.amount.MulRate(tt.rate); got != tt.want {
			t.Errorf("%d.MulRate(%d) = %d, want %d", tt.amount, tt.rate, got, tt.want)
		}
	}
}

func TestMoneyString(t *testing.T) {
	tests := []struct {
		m    Money
		want string
	}{
		{0, "0,00"},
		{5, "0,05"},
		{-5, "-0,05"},
		{1299, "12,99"},
		{100000, "1.000,00"},
		{123456789, "1.234.567,89"},
		{-123456789, "-1.234.567,89"},
		{math.MinInt64, "-92.233.720.368.547.758,08"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("Money(%d).String() = %q, want %q", int64(tt.m), got, tt.want)
		}
	}
}

func TestUnits(t *testing.T) {
	if got := Units(3); got != 3000 {
		t.Errorf("Units(3) = %d, want 3000", got)
	}
}
