// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package text

import (
	"slices"
	"strings"
	"testing"
)

func TestSortKeyTurkishOrder(t *testing.T) {
	want := []string{
		"Cips", "Çikolata 80 g", "Domates", "Ekmek 250 g", "Gofret", "Ğ harfli", "Irmak", "İncir",
		"Su 1,5 L", "Su 10 L", "Sucuk", "Şampuan 500 ml", "Un", "Üzüm", "Zeytin",
	}
	got := slices.Clone(want)
	slices.Reverse(got)
	slices.SortFunc(got, func(a, b string) int { return strings.Compare(SortKey(a), SortKey(b)) })
	if !slices.Equal(got, want) {
		t.Errorf("order:\n%q\nwant\n%q", got, want)
	}
	// Upper/lower case and circumflex do not change the order.
	if SortKey("ŞAMPUAN") != SortKey("şampuan") || SortKey("Kâse") != SortKey("kase") {
		t.Error("upper/lower case or a circumflex changed the order")
	}
}

func TestFold(t *testing.T) {
	for in, want := range map[string]string{" ŞAMPUAN ": "şampuan", "IŞIK": "ışık", "İncir": "incir"} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, istenen %q", in, got, want)
		}
	}
}
