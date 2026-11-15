// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the receipt rules of the fiscal printer simulator: validating the
// receipt, the simulator number and the text of the receipt that has no fiscal value.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/layout"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// Warning is the first and last line of the simulator receipt. It must not be removed
// (see CONTRIBUTING.md): the simulator receipt does not stand in for a fiscal document
// in live sales.
const Warning = "MALİ DEĞERİ YOKTUR"

// MinWidth is the narrowest column width of the receipt (a 58 mm printer).
const MinWidth = 32

var ErrInvalidReceipt = errors.New("geçersiz fiş")

// Line is an item on the receipt. Prices include VAT.
type Line struct {
	Name      string
	Qty       money.Qty
	UnitPrice money.Money
	Total     money.Money
	TaxRate   money.Rate
}

// TaxLine is the total and the tax amount that fall on one VAT rate.
type TaxLine struct {
	Rate  money.Rate
	Gross money.Money
	Tax   money.Money
}

// Payment is a payment made on the receipt. Method is "cash", "card" or
// "meal_card" (a meal card or voucher).
type Payment struct {
	Method string
	Amount money.Money
}

// Receipt is the content of the receipt to be printed. sales computes the amounts; the simulator
// only checks that they are consistent.
type Receipt struct {
	SaleID     string
	RegisterID string
	Lines      []Line
	Taxes      []TaxLine
	Total      money.Money
	Payments   []Payment
	Change     money.Money
	// At is the moment the receipt was issued; it is printed in the time
	// zone of this time on the receipt.
	At time.Time
}

// HasCash says whether the receipt has a cash payment (the drawer is opened).
func (r Receipt) HasCash() bool {
	for _, p := range r.Payments {
		if p.Method == paymethod.Cash {
			return true
		}
	}
	return false
}

// TaxTotal is the total VAT on the receipt (TOPKDV).
func (r Receipt) TaxTotal() money.Money {
	var total money.Money
	for _, t := range r.Taxes {
		total += t.Tax
	}
	return total
}

// Validate checks that the receipt is consistent within itself (the rule is in
// pkg/calc: the items, the VAT breakdown and the payments must add up to the receipt total).
func (r Receipt) Validate() error {
	h := calc.Receipt{Total: r.Total, Change: r.Change}
	for _, l := range r.Lines {
		h.Items = append(h.Items, calc.Item{Total: l.Total, Rate: l.TaxRate})
	}
	for _, t := range r.Taxes {
		h.Taxes = append(h.Taxes, calc.Tax{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
	}
	for _, p := range r.Payments {
		h.Payments = append(h.Payments, calc.Payment{Method: p.Method, Amount: p.Amount})
	}
	if err := h.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReceipt, strings.TrimPrefix(err.Error(), calc.ErrInconsistent.Error()+": "))
	}
	return nil
}

// FormatFiscalNo formats the simulator number: 1 → "SIM-000001".
func FormatFiscalNo(n int64) string { return fmt.Sprintf("SIM-%06d", n) }

// Layout holds the format settings of the receipt.
type Layout struct {
	// Width is the column width: 32 for 58 mm, 48 for 80 mm.
	Width int
	// Header is the business information at the top of the receipt: the sign name, address,
	// tax office and tax number (VKN/TCKN), phone.
	Header []string
	// Location is the time zone the receipt time is printed in (e.g. Europe/Istanbul);
	// if empty, Receipt.At is printed in its own time zone.
	Location *time.Location
}

// Render prepares the receipt that has no fiscal value as plain text (UTF-8).
// Converting it to the printer's code page is the job of the device service.
//
// The layout follows the GİB (Revenue Administration) YN ÖKC receipt example (YN ÖKC Receipt, e-Document and Report
// Formats Technical Guide Draft, Version 1.0, section 2): the business
// information, date and time, receipt number, the items with their rates, TOPKDV,
// TOPLAM and the payments. Marks that belong only to a real fiscal receipt, such as the fiscal
// symbol, the EKÜ and the Z number, are deliberately not printed; the first and last line are always
// Warning.
func Render(r Receipt, fiscalNo string, l Layout) string {
	width := max(l.Width, MinWidth)
	var b strings.Builder
	line := func(ss ...string) {
		for _, s := range ss {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	sep := layout.Rule(width)

	line(layout.Center(Warning, width)...)
	line(layout.Center("ÖKC SİMÜLATÖRÜ", width)...)
	for _, h := range l.Header {
		line(layout.Center(h, width)...)
	}
	line(sep)
	at := r.At
	if l.Location != nil {
		at = at.In(l.Location)
	}
	line(layout.Pair(at.Format("02.01.2006"), "SAAT: "+at.Format("15.04"), width)...)
	line("FİŞ NO: " + fiscalNo)
	line(sep)
	for _, it := range r.Lines {
		if it.Qty != money.QtyScale {
			line("  " + formatQty(it.Qty) + " x " + it.UnitPrice.String())
		}
		rate, amount := formatRate(it.TaxRate), "*"+it.Total.String()
		if fits(it.Name+" "+rate, amount, width) {
			line(layout.Pair(it.Name+" "+rate, amount, width)...)
			continue
		}
		line(layout.Wrap(it.Name, width)...)
		line(layout.Pair("  "+rate, amount, width)...)
	}
	line(sep)
	line(layout.Pair("TOPKDV", "*"+r.TaxTotal().String(), width)...)
	line(layout.Pair("TOPLAM", "*"+r.Total.String(), width)...)
	line(sep)
	for _, p := range r.Payments {
		line(layout.Pair(paymentLabel(p.Method), "*"+p.Amount.String(), width)...)
	}
	if r.Change > 0 {
		line(layout.Pair("PARA ÜSTÜ", "*"+r.Change.String(), width)...)
	}
	line(sep)
	line(layout.Center(Warning, width)...)
	return b.String()
}

// fits says whether the left and right text fit on one line with at least one space
// between them.
func fits(left, right string, width int) bool {
	return utf8.RuneCountInString(left)+1+utf8.RuneCountInString(right) <= width
}

// formatQty formats the quantity: 1000 → "1", 1250 → "1,250".
func formatQty(q money.Qty) string {
	whole, frac := q/money.QtyScale, q%money.QtyScale
	if frac == 0 {
		return fmt.Sprint(int64(whole))
	}
	return fmt.Sprintf("%d,%03d", whole, frac)
}

// formatRate formats the VAT rate: 2000 → "%20", 150 → "%1,5".
func formatRate(r money.Rate) string {
	whole, frac := r/100, r%100
	if frac == 0 {
		return fmt.Sprintf("%%%d", whole)
	}
	return strings.TrimRight(fmt.Sprintf("%%%d,%02d", whole, frac), "0")
}

// paymentLabel returns the payment type by its name in the GİB receipt example.
func paymentLabel(method string) string {
	switch method {
	case paymethod.Cash:
		return "NAKİT"
	case paymethod.Card:
		return "KREDİ KARTI"
	case paymethod.MealCard:
		return "YEMEK KARTI"
	}
	return strings.ToUpper(method)
}
