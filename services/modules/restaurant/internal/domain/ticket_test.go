// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"strings"
	"testing"
	"unicode/utf8"

	"abarow.com/pkg/menu"
)

// Tickets do not exceed the printer's columns (the printer rejects a wide line); the kitchen
// ticket has the options and note, the check has the total and "MALİ DEĞERİ YOKTUR".
func TestTickets(t *testing.T) {
	o := NewOrder("o1", "m1", "Ali", at)
	add(t, o, "l-1", meatball, 2, menu.Choice{Group: "pisirme", Options: []string{"az"}}, menu.Choice{Group: "ekstra", Options: []string{"kasar"}})
	o.SetLine("l-1", 2, "soğansız, yanında bol yeşillik ve közlenmiş biber")
	add(t, o, "l-2", Product{ID: "x", Name: "Çok uzun adlı bir ürün: fırında kaşarlı mantar dolması ve yanında pilav", UnitPrice: 15000}, 1)
	o.Send(at, "")
	for _, cols := range []int{32, 48} {
		_, kitchen := KitchenTicket(cols, menu.Kitchen, "Bahçe 12", "Ali", at, o.Lines, false)
		_, bill := BillDocument(cols, "Bahçe 12", "Ali", at, o)
		for _, doc := range [][]string{kitchen, bill} {
			for _, line := range doc {
				if utf8.RuneCountInString(line) > cols {
					t.Errorf("%d columns: a line overflows (%d): %q", cols, utf8.RuneCountInString(line), line)
				}
			}
		}
		k := strings.Join(kitchen, "\n")
		if !strings.Contains(k, "2 x Köfte") || !strings.Contains(k, "- az pişmiş") || !strings.Contains(k, "NOT: soğansız") {
			t.Errorf("kitchen ticket:\n%s", k)
		}
		b := strings.Join(bill, "\n")
		if strings.Count(b, NotFiscal) != 2 || !strings.Contains(b, "TOPLAM") || !strings.Contains(b, "840,00") {
			t.Errorf("adisyon:\n%s", b)
		}
	}
	title, voidDoc := KitchenTicket(32, menu.Bar, "1", "Ali", at, []Line{{Qty: 1, Name: "Ayran", VoidReason: "döküldü"}}, true)
	if !strings.HasPrefix(title, "İPTAL Bar") || !strings.Contains(strings.Join(voidDoc, "\n"), "NEDEN: döküldü") {
		t.Errorf("cancellation ticket %s:\n%s", title, strings.Join(voidDoc, "\n"))
	}
}
