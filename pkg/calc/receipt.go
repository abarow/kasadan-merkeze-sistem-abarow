// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package calc is the sale's calculation engine: the item total, separating VAT
// by rate, whether a payment can close the sale, change and checking that a
// receipt is consistent in itself. The service that builds the sale
// (sales), the modules that collect the basket and the check (retail, restaurant) and
// the ÖKC adapters that take the receipt (simulator, Beko) use the same rule; the rule
// is written once, so no rounding difference arises.
//
// Amounts are in kuruş, quantity in thousandths, the VAT rate in base points (pkg/money).
package calc

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

var (
	// ErrInvalidPayment means the payment itself is invalid (the amount is not positive,
	// the method is unknown).
	ErrInvalidPayment = errors.New("geçersiz ödeme")
	// ErrInsufficientPayment means the payments total is less than the sale amount.
	ErrInsufficientPayment = errors.New("ödeme toplamı satış tutarından az")
	// ErrExcessNonCash means the excess payment was made with a method other than cash.
	ErrExcessNonCash = errors.New("fazla ödeme yalnızca nakitle yapılabilir")
	// ErrInconsistent means the receipt's totals do not match one another.
	ErrInconsistent = errors.New("fiş tutarsız")
	// ErrTooLarge means a quantity, price, amount or count is above the limits below.
	ErrTooLarge = errors.New("miktar, fiyat ya da tutar çok büyük")
)

// Limits of a sale. The arithmetic is int64 kuruş: with these limits no line, total or
// payment sum can overflow (1000 lines × 10¹⁴ = 10¹⁷; 50 payments × 10¹⁷ = 5·10¹⁸ < 9.2·10¹⁸).
// They are far above any real sale; they exist so that a wrong or hostile input is
// rejected instead of overflowing.
const (
	// MaxQty is the largest quantity of a line: 100 000 pieces or 100 t.
	MaxQty money.Qty = 100_000 * money.QtyScale
	// MaxUnitPrice is the largest unit price: 10 000 000 TL.
	MaxUnitPrice money.Money = 10_000_000 * 100
	// MaxLines is the largest number of lines of a sale.
	MaxLines = 1000
	// MaxPayments is the largest number of payments of a sale.
	MaxPayments = 50
	// MaxPayment is the largest single payment: the largest possible sale total.
	MaxPayment = money.Money(MaxLines) * MaxUnitPrice * money.Money(MaxQty/money.QtyScale)
)

// CheckLine checks that a line's quantity and unit price are within the limits.
func CheckLine(unit money.Money, qty money.Qty) error {
	if qty > MaxQty || unit > MaxUnitPrice {
		return fmt.Errorf("%w: miktar en çok %d, birim fiyat en çok %s TL", ErrTooLarge, MaxQty/money.QtyScale, MaxUnitPrice)
	}
	return nil
}

// Item is an item that enters the calculation: its VAT-included amount and VAT rate.
type Item struct {
	Total money.Money
	Rate  money.Rate
}

// Line computes the item's amount from the unit price and quantity (VAT included).
func Line(unit money.Money, qty money.Qty) money.Money { return unit.MulQty(qty) }

// Sum is the VAT-included total of the items.
func Sum(items []Item) money.Money {
	var total money.Money
	for _, it := range items {
		total += it.Total
	}
	return total
}

// Tax is the VAT-included total and tax amount that fall on a VAT rate.
type Tax struct {
	Rate  money.Rate
	Gross money.Money
	Tax   money.Money
}

// Taxes computes VAT by rate, over each rate's VAT-included total.
// The YN ÖKC receipt and the Z report also sum VAT by rate (GİB's YN
// ÖKC receipt and report formats guide); so the kuruş differences that computing line
// by line and summing would cause do not arise. The result is in ascending
// order of rate.
func Taxes(items []Item) []Tax {
	var out []Tax
	for _, it := range items {
		i := slices.IndexFunc(out, func(t Tax) bool { return t.Rate == it.Rate })
		if i < 0 {
			out = append(out, Tax{Rate: it.Rate})
			i = len(out) - 1
		}
		out[i].Gross += it.Total
	}
	for i := range out {
		out[i].Tax = out[i].Gross.IncludedTax(out[i].Rate)
	}
	slices.SortFunc(out, func(a, b Tax) int { return cmp.Compare(a.Rate, b.Rate) })
	return out
}

// TaxTotal is the total tax in the VAT breakdown (TOPKDV on the receipt).
func TaxTotal(taxes []Tax) money.Money {
	var total money.Money
	for _, t := range taxes {
		total += t.Tax
	}
	return total
}

// Payment is a payment. Method is a paymethod name (cash, card, meal_card).
type Payment struct {
	Method string
	Amount money.Money
}

// Paid is the total of the payments.
func Paid(payments []Payment) money.Money {
	var total money.Money
	for _, p := range payments {
		total += p.Amount
	}
	return total
}

// CheckPayments checks whether the payments can close the sale of the total
// amount. Each amount is positive and the method is known; the payments
// total cannot be less than the sale; an excess payment can only be in cash
// (the excess is given as change, it cannot exceed the cash amount). It is called
// before the card is charged.
func CheckPayments(total money.Money, payments []Payment) error {
	if len(payments) > MaxPayments {
		return fmt.Errorf("%w: en çok %d ödeme", ErrTooLarge, MaxPayments)
	}
	var paid, cash money.Money
	for _, p := range payments {
		if p.Amount <= 0 {
			return fmt.Errorf("%w: tutar pozitif olmalı", ErrInvalidPayment)
		}
		if p.Amount > MaxPayment {
			return fmt.Errorf("%w: ödeme en çok %s TL", ErrTooLarge, MaxPayment)
		}
		switch p.Method {
		case paymethod.Cash:
			cash += p.Amount
		case paymethod.Card, paymethod.MealCard:
		default:
			return fmt.Errorf("%w: bilinmeyen yöntem %q", ErrInvalidPayment, p.Method)
		}
		paid += p.Amount
	}
	if paid < total {
		return ErrInsufficientPayment
	}
	if paid-total > cash {
		return ErrExcessNonCash
	}
	return nil
}

// Change is the change to be given to the customer (paid − total).
func Change(total money.Money, payments []Payment) money.Money { return Paid(payments) - total }

// Receipt is the totals of an issued receipt; the ÖKC adapter checks with Validate that it
// is consistent in itself before sending the receipt to the device.
type Receipt struct {
	Items    []Item
	Taxes    []Tax
	Payments []Payment
	Total    money.Money
	Change   money.Money
}

// Validate checks that the total of the items equals the receipt total, the VAT breakdown's (VAT-included
// amounts) total equals the receipt total, and the payments total minus the change equals the
// receipt total. If the VAT breakdown is empty (if the VAT calculation is done on the side that
// takes the receipt) its check is skipped.
func (r Receipt) Validate() error {
	if len(r.Items) == 0 {
		return fmt.Errorf("%w: kalem yok", ErrInconsistent)
	}
	if total := Sum(r.Items); total != r.Total {
		return fmt.Errorf("%w: kalemlerin toplamı %s, fiş toplamı %s", ErrInconsistent, total, r.Total)
	}
	if len(r.Taxes) > 0 {
		var gross money.Money
		for _, t := range r.Taxes {
			gross += t.Gross
		}
		if gross != r.Total {
			return fmt.Errorf("%w: KDV dökümünün toplamı %s, fiş toplamı %s", ErrInconsistent, gross, r.Total)
		}
	}
	if paid := Paid(r.Payments); paid-r.Total != r.Change {
		return fmt.Errorf("%w: ödeme %s, toplam %s, para üstü %s", ErrInconsistent, paid, r.Total, r.Change)
	}
	return nil
}
