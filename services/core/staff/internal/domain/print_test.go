// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func checkWidth(t *testing.T, lines []string, columns int) {
	t.Helper()
	for i, l := range lines {
		if utf8.RuneCountInString(l) > columns {
			t.Errorf("line %d exceeds %d columns: %q", i+1, columns, l)
		}
	}
	if lines[len(lines)-1] != strings.Repeat(" ", (columns-utf8.RuneCountInString(PrintWarning))/2)+PrintWarning {
		t.Errorf("the last line is not the warning: %q", lines[len(lines)-1])
	}
}

func TestRenderShiftReport(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	opened := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	r := ShiftReport{
		Shift: Shift{ID: 11, CashierName: "Kasiyer Ayşe", OpenedAt: opened, ClosedAt: opened.Add(8 * time.Hour),
			OpeningCash: 50000, CountedCash: 64878},
		Totals: Totals{ReceiptCount: 1, Total: 11978, Payments: []PaymentTotal{{Method: Cash, Amount: 11978}, {Method: Card}}},
		CashIn: 5000, CashOut: 2000, ExpectedCash: 64978, Difference: -100,
	}
	for _, columns := range []int{32, 48} {
		lines := RenderShiftReport(r, columns, ist, opened.Add(9*time.Hour))
		checkWidth(t, lines, columns)
		for _, want := range []string{"VARDİYA RAPORU", "No 11 - Kasiyer Ayşe", "09:00", "649,78", "Kasa açığı"} {
			if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
				t.Errorf("%d columns: no %q:\n%s", columns, want, strings.Join(lines, "\n"))
			}
		}
	}
}

func TestRenderDayReport(t *testing.T) {
	opened := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	r := DayReport{Period: Period{ZNo: 5, OpenedAt: opened}, Totals: Totals{
		ReceiptCount: 2, FirstReceiptNo: 11, LastReceiptNo: 12, Total: 17437, TaxTotal: 790,
		Taxes:    []TaxTotal{{Rate: 100, Gross: 12438, Tax: 123}, {Rate: 150, Gross: 1, Tax: 0}, {Rate: 2000, Gross: 2799, Tax: 467}},
		Payments: []PaymentTotal{{Method: Cash, Amount: 15437}, {Method: Card, Amount: 2000}},
	}}
	x := RenderDayReport(r, 32, time.UTC, opened)
	checkWidth(t, x, 32)
	if strings.TrimSpace(x[0]) != "X RAPORU" || slices.ContainsFunc(x, func(l string) bool { return strings.Contains(l, "Dönem sonu") }) {
		t.Errorf("X raporu:\n%s", strings.Join(x, "\n"))
	}

	r.ClosedAt = opened.Add(16 * time.Hour)
	z := RenderDayReport(r, 32, time.UTC, opened)
	checkWidth(t, z, 32)
	for _, want := range []string{"Z RAPORU", "Z NO: 5", "Dönem sonu", "2 (11-12)", "KDV %1,5", "TOPLAM", "174,37", "Resmi Z raporu ÖKC raporudur"} {
		if !slices.ContainsFunc(z, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("no %q in the Z report:\n%s", want, strings.Join(z, "\n"))
		}
	}
}
