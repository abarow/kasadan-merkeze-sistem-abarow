// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"abarow.com/pkg/money"
)

var layout48 = Layout{Width: 48, Header: []string{"ÖRNEK MARKET", "VKN: 1234567890"}}

func testReceipt() Receipt {
	return Receipt{
		SaleID:     "s1",
		RegisterID: "register-1",
		Lines: []Line{
			{Name: "Deterjan 1 L", Qty: 1000, UnitPrice: 1299, Total: 1299, TaxRate: 2000},
			{Name: "Domates", Qty: 1250, UnitPrice: 8990, Total: 11238, TaxRate: 100},
		},
		Taxes: []TaxLine{
			{Rate: 100, Gross: 11238, Tax: 111},
			{Rate: 2000, Gross: 1299, Tax: 217},
		},
		Total:    12537,
		Payments: []Payment{{Method: "cash", Amount: 20000}},
		Change:   7463,
		At:       time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC),
	}
}

// gibReceipt is the integrated receipt example in the GİB YN ÖKC receipt format guide
// (Technical Guide Draft Version 1.0, section 2.2).
func gibReceipt() Receipt {
	return Receipt{
		Lines: []Line{
			{Name: "EKMEK", Qty: 1000, UnitPrice: 1200, Total: 1200, TaxRate: 100},
			{Name: "AYAKKABI", Qty: 1000, UnitPrice: 2200, Total: 2200, TaxRate: 1000},
			{Name: "ŞAMPUAN", Qty: 1000, UnitPrice: 1500, Total: 1500, TaxRate: 2000},
		},
		Taxes: []TaxLine{
			{Rate: 100, Gross: 1200, Tax: 12},
			{Rate: 1000, Gross: 2200, Tax: 200},
			{Rate: 2000, Gross: 1500, Tax: 250},
		},
		Total:    4900,
		Payments: []Payment{{Method: "cash", Amount: 2900}, {Method: "card", Amount: 2000}},
		At:       time.Date(2026, 4, 1, 8, 30, 0, 0, time.UTC),
	}
}

func lines(out string) []string { return strings.Split(strings.TrimRight(out, "\n"), "\n") }

// normalize reduces the spaces in a line to one; for a comparison
// independent of alignment.
func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestRenderFollowsGIBLayout(t *testing.T) {
	out := Render(gibReceipt(), "SIM-000002", Layout{Width: MinWidth, Header: []string{"ÖRNEK MARKET"}})
	var got []string
	for _, l := range lines(out) {
		got = append(got, normalize(l))
	}
	sep := strings.Repeat("-", MinWidth)
	want := []string{
		Warning,
		"ÖKC SİMÜLATÖRÜ",
		"ÖRNEK MARKET",
		sep,
		"01.04.2026 SAAT: 08.30",
		"FİŞ NO: SIM-000002",
		sep,
		"EKMEK %1 *12,00",
		"AYAKKABI %10 *22,00",
		"ŞAMPUAN %20 *15,00",
		sep,
		"TOPKDV *4,62",
		"TOPLAM *49,00",
		sep,
		"NAKİT *29,00",
		"KREDİ KARTI *20,00",
		sep,
		Warning,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("receipt layout differs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Amounts are right-aligned: lines with an amount are at full width.
	for _, l := range lines(out) {
		if strings.Contains(l, "*") && utf8.RuneCountInString(l) != MinWidth {
			t.Errorf("not right-aligned (%d characters): %q", utf8.RuneCountInString(l), l)
		}
	}
}

func TestRenderUsesLocation(t *testing.T) {
	ist := time.FixedZone("TRT", 3*60*60) // Türkiye is UTC+3, there is no daylight saving time
	out := Render(testReceipt(), "SIM-000001", Layout{Width: 48, Location: ist})
	if !strings.Contains(out, "SAAT: 12.30") {
		t.Errorf("09:30 UTC should print as 12.30 Turkey time:\n%s", out)
	}
}

func TestRenderWarningIsFirstAndLastLine(t *testing.T) {
	for _, width := range []int{MinWidth, 48} {
		ls := lines(Render(testReceipt(), "SIM-000001", Layout{Width: width}))
		if first := strings.TrimSpace(ls[0]); first != Warning {
			t.Errorf("width %d: first line %q, want %q", width, first, Warning)
		}
		if last := strings.TrimSpace(ls[len(ls)-1]); last != Warning {
			t.Errorf("width %d: last line %q, want %q", width, last, Warning)
		}
	}
}

func TestRenderContents(t *testing.T) {
	out := Render(testReceipt(), "SIM-000001", layout48)
	for _, want := range []string{
		"ÖRNEK MARKET", "VKN: 1234567890", "25.10.2026", "SAAT: 09.30", "FİŞ NO: SIM-000001",
		"Deterjan 1 L %20", "*12,99", "1,250 x 89,90", "Domates %1", "*112,38",
		"TOPKDV", "*3,28", "TOPLAM", "*125,37", "NAKİT", "*200,00", "PARA ÜSTÜ", "*74,63",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q on the receipt:\n%s", want, out)
		}
	}
	if strings.Contains(out, "1 x 12,99") {
		t.Errorf("the quantity line should not be printed for a 1-unit item:\n%s", out)
	}
}

func TestRenderFitsWidth(t *testing.T) {
	r := testReceipt()
	r.Lines[0].Name = "Çamaşır Deterjanı Sıvı Lavanta Ferahlığı 2,5 Litre"
	for _, width := range []int{MinWidth, 48} {
		out := Render(r, "SIM-000001", Layout{Width: width, Header: []string{"Çok Uzun Bir Tabela Adı Olan Örnek Market Ltd. Şti."}})
		if !strings.Contains(strings.ReplaceAll(out, "\n", ""), r.Lines[0].Name) {
			t.Errorf("width %d: the long product name disappeared:\n%s", width, out)
		}
		for _, l := range lines(out) {
			if n := utf8.RuneCountInString(l); n > width {
				t.Errorf("width %d: line of %d characters: %q", width, n, l)
			}
		}
	}
}

// A meal card is a separate payment line on the receipt, under its name in the GİB example.
func TestRenderMealCard(t *testing.T) {
	r := testReceipt()
	r.Payments = []Payment{{Method: "meal_card", Amount: r.Total}}
	r.Change = 0
	out := Render(r, "SIM-000001", Layout{Width: MinWidth})
	if !strings.Contains(out, "YEMEK KARTI") || strings.Contains(out, "KREDİ KARTI") {
		t.Errorf("no meal card line:\n%s", out)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Receipt)
		ok     bool
	}{
		{"tutarlı", func(*Receipt) {}, true},
		{"kalemsiz", func(r *Receipt) { r.Lines = nil }, false},
		{"toplam tutmuyor", func(r *Receipt) { r.Total = 12538 }, false},
		{"KDV dökümü tutmuyor", func(r *Receipt) { r.Taxes = r.Taxes[:1] }, false},
		{"para üstü tutmuyor", func(r *Receipt) { r.Change = 7000 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := testReceipt()
			tt.change(&r)
			err := r.Validate()
			if tt.ok && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidReceipt) {
				t.Errorf("err = %v, want %v", err, ErrInvalidReceipt)
			}
		})
	}
}

func TestFormatFiscalNo(t *testing.T) {
	if got := FormatFiscalNo(1); got != "SIM-000001" {
		t.Errorf("FormatFiscalNo(1) = %q", got)
	}
	if got := FormatFiscalNo(1234567); got != "SIM-1234567" {
		t.Errorf("FormatFiscalNo(1234567) = %q", got)
	}
}

func TestFormatQtyAndRate(t *testing.T) {
	qtys := map[money.Qty]string{1000: "1", 3000: "3", 1250: "1,250", 5: "0,005"}
	for q, want := range qtys {
		if got := formatQty(q); got != want {
			t.Errorf("formatQty(%d) = %q, istenen %q", q, got, want)
		}
	}
	rates := map[money.Rate]string{2000: "%20", 100: "%1", 150: "%1,5", 0: "%0", 1025: "%10,25"}
	for r, want := range rates {
		if got := formatRate(r); got != want {
			t.Errorf("formatRate(%d) = %q, istenen %q", r, got, want)
		}
	}
}
