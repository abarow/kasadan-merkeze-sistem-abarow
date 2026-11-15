// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/layout"
	"abarow.com/pkg/money"
)

// PrintWarning is the last line of the reports printed: the reports are a management
// summary, not a fiscal document. It must not be removed (see CONTRIBUTING.md).
const PrintWarning = "MALİ DEĞERİ YOKTUR"

// printer collects the lines without exceeding the width.
type printer struct {
	width int
	loc   *time.Location
	lines []string
}

func (p *printer) center(s string)                { p.lines = append(p.lines, layout.Center(s, p.width)...) }
func (p *printer) pair(l, r string)               { p.lines = append(p.lines, layout.Pair(l, r, p.width)...) }
func (p *printer) amount(l string, m money.Money) { p.pair(l, m.String()) }
func (p *printer) rule()                          { p.lines = append(p.lines, layout.Rule(p.width)) }
func (p *printer) time(t time.Time) string {
	if p.loc != nil {
		t = t.In(p.loc)
	}
	return t.Format("02.01.2006 15:04")
}

func (p *printer) footer(printedAt time.Time) []string {
	p.rule()
	p.pair("Basım", p.time(printedAt))
	p.center(PrintWarning)
	return p.lines
}

func (p *printer) payments(payments []PaymentTotal) {
	for _, pay := range payments {
		label := "Diğer"
		switch pay.Method {
		case Cash:
			label = "Nakit"
		case Card:
			label = "Kredi kartı"
		case MealCard:
			label = "Yemek kartı"
		}
		p.amount(label, pay.Amount)
	}
}

// RenderShiftReport lays out the shift report as lines for a printer with columns
// columns; times are printed in loc.
func RenderShiftReport(r ShiftReport, columns int, loc *time.Location, printedAt time.Time) []string {
	p := &printer{width: columns, loc: loc}
	p.center("VARDİYA RAPORU")
	p.center(fmt.Sprintf("No %d - %s", r.ID, r.CashierName))
	p.rule()
	p.pair("Açılış", p.time(r.OpenedAt))
	p.pair("Kapanış", p.time(r.ClosedAt))
	p.pair("Fiş sayısı", fmt.Sprint(r.ReceiptCount))
	p.amount("Satış toplamı", r.Total)
	p.payments(r.Payments)
	p.pair("İptal", fmt.Sprintf("%d (%s)", r.VoidedCount, r.VoidedTotal))
	p.rule()
	p.amount("Açılış nakdi", r.OpeningCash)
	p.amount("Kasaya konan", r.CashIn)
	p.amount("Kasadan alınan", r.CashOut)
	p.pair("Satışsız çekmece açılışı", fmt.Sprint(r.DrawerOpens))
	p.amount("Beklenen nakit", r.ExpectedCash)
	p.amount("Sayılan nakit", r.CountedCash)
	label := "Fark"
	switch {
	case r.Difference < 0:
		label = "Kasa açığı"
	case r.Difference > 0:
		label = "Kasa fazlası"
	}
	p.amount(label, r.Difference)
	return p.footer(printedAt)
}

// RenderDayReport lays out the X or Z report (Z if the period is closed) as lines for a
// printer with columns columns. The report is a management summary; while a real ÖKC is
// connected the official Z report is the ÖKC's own report.
func RenderDayReport(r DayReport, columns int, loc *time.Location, printedAt time.Time) []string {
	p := &printer{width: columns, loc: loc}
	kind := "X"
	if !r.ClosedAt.IsZero() {
		kind = "Z"
	}
	p.center(kind + " RAPORU")
	p.center("(YÖNETİM ÖZETİ)")
	p.center(fmt.Sprintf("Z NO: %d", r.ZNo))
	p.rule()
	p.pair("Dönem başı", p.time(r.OpenedAt))
	if kind == "Z" {
		p.pair("Dönem sonu", p.time(r.ClosedAt))
	}
	receipts := fmt.Sprint(r.ReceiptCount)
	if r.ReceiptCount > 0 {
		receipts += fmt.Sprintf(" (%d-%d)", r.FirstReceiptNo, r.LastReceiptNo)
	}
	p.pair("Fiş sayısı", receipts)
	p.rule()
	for _, t := range r.Taxes {
		p.amount("KDV "+formatRate(t.Rate)+" toplam", t.Gross)
		p.amount("KDV "+formatRate(t.Rate), t.Tax)
	}
	p.amount("TOPKDV", r.TaxTotal)
	p.amount("TOPLAM", r.Total)
	p.rule()
	p.payments(r.Payments)
	p.pair("İptal", fmt.Sprintf("%d (%s)", r.VoidedCount, r.VoidedTotal))
	if kind == "Z" {
		p.rule()
		p.center("Resmi Z raporu ÖKC raporudur")
	}
	return p.footer(printedAt)
}

// formatRate formats the VAT rate: 2000 → "%20", 150 → "%1,5".
func formatRate(r money.Rate) string {
	whole, frac := r/100, r%100
	if frac == 0 {
		return fmt.Sprintf("%%%d", whole)
	}
	return strings.TrimRight(fmt.Sprintf("%%%d,%02d", whole, frac), "0")
}
