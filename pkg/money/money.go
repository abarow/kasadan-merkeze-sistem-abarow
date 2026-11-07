// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package money provides integer-based types for money, rate and quantity.
//
// Since floating point numbers cannot hold decimal values exactly
// (0.1 + 0.2 = 0.30000000000000004) money is kept everywhere in kuruş as an int64.
// All rounding follows a single rule: half is rounded away from zero
// (2.5 → 3, -2.5 → -3).
package money

import (
	"math"
	"math/bits"
	"strconv"
)

// Money is an amount of money in kuruş: 12.99 TL = 1299.
type Money int64

// Rate is a rate in base points: 20% = 2000.
type Rate int64

// Qty is a quantity in thousandths of a unit: 1 piece = 1000, 1.250 kg = 1250.
type Qty int64

const (
	// RateScale is the Rate value that corresponds to 100%.
	RateScale Rate = 10000
	// QtyScale is the Qty value that corresponds to one unit (1 piece, 1 kg).
	QtyScale Qty = 1000
)

const overflowMsg = "money: sonuç int64 sınırını aşıyor"

// Units turns a whole number of units into Qty: Units(3) = 3 pieces.
func Units(n int64) Qty { return Qty(n) * QtyScale }

// MulQty multiplies the unit price by the quantity and returns the line amount.
// 89.90 TL/kg × 1.250 kg = 112.38 TL.
func (m Money) MulQty(q Qty) Money {
	return Money(mulDivRound(int64(m), int64(q), int64(QtyScale)))
}

// MulRate returns the given rate of the amount; used for a discount amount or the
// VAT to be added to a price excluding VAT. 15.55 TL × 10% = 1.56 TL.
func (m Money) MulRate(r Rate) Money {
	return Money(mulDivRound(int64(m), int64(r), int64(RateScale)))
}

// IncludedTax separates the VAT within a VAT-included amount:
// gross × rate / (100% + rate). 120.00 TL, 20% → 20.00 TL.
func (m Money) IncludedTax(r Rate) Money {
	if r < 0 {
		panic("money: KDV oranı negatif olamaz")
	}
	return Money(mulDivRound(int64(m), int64(r), int64(RateScale+r)))
}

// String returns the amount in Turkish format: 1234567 → "12.345,67".
func (m Money) String() string {
	neg := m < 0
	u := abs(int64(m))
	digits := strconv.FormatUint(u/100, 10)
	frac := u % 100

	b := make([]byte, 0, len(digits)+len(digits)/3+4)
	if neg {
		b = append(b, '-')
	}
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b = append(b, '.')
		}
		b = append(b, digits[i])
	}
	b = append(b, ',', byte('0'+frac/10), byte('0'+frac%10))
	return string(b)
}

// divRound returns the quotient num/den rounding half away from zero.
func divRound(num, den int64) int64 { return mulDivRound(num, 1, den) }

// mulDivRound computes a×b/den with a 128-bit intermediate result, so that it gives the
// right result even if a×b does not fit in int64. Half is rounded away from zero.
// den must be positive. It panics if the result does not fit in int64; with real
// amounts this happens only through a programming error.
func mulDivRound(a, b, den int64) int64 {
	if den <= 0 {
		panic("money: payda pozitif olmalı")
	}
	neg := (a < 0) != (b < 0)
	d := uint64(den)
	hi, lo := bits.Mul64(abs(a), abs(b))
	if hi >= d {
		panic(overflowMsg)
	}
	q, r := bits.Div64(hi, lo, d)
	if q > 1<<63 {
		panic(overflowMsg)
	}
	if r >= d-r {
		q++
	}
	if neg {
		if q > 1<<63 {
			panic(overflowMsg)
		}
		return -int64(q)
	}
	if q > math.MaxInt64 {
		panic(overflowMsg)
	}
	return int64(q)
}

// abs returns the absolute value of x; it works for math.MinInt64 too.
func abs(x int64) uint64 {
	u := uint64(x)
	if x < 0 {
		u = -u
	}
	return u
}
