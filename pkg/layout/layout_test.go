// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package layout

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPair(t *testing.T) {
	if got := Pair("Nakit", "119,78", 16); !reflect.DeepEqual(got, []string{"Nakit     119,78"}) {
		t.Errorf("single line: %q", got)
	}
	// A line that does not fit is split; the width is not exceeded.
	got := Pair("Çok uzun bir açıklama", "1.234,56", 16)
	want := []string{"Çok uzun bir açı", "klama", "        1.234,56"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("split line: %q, want %q", got, want)
	}
}

func TestCenterAndWrap(t *testing.T) {
	if got := Center("ŞŞ", 6); !reflect.DeepEqual(got, []string{"  ŞŞ"}) {
		t.Errorf("centering (character count): %q", got)
	}
	for _, l := range Center(strings.Repeat("ğ", 40), 16) {
		if utf8.RuneCountInString(l) > 16 {
			t.Errorf("line exceeding the width: %q", l)
		}
	}
	if got := Wrap("", 16); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("empty text: %q", got)
	}
}

// Word-by-word splitting: a word is not cut in the middle (except a single word that does
// not fit); indented lines are aligned; no line exceeds the width.
func TestWordsAndIndent(t *testing.T) {
	if got := Words("Çok uzun adlı bir ürün: fırında kaşarlı mantar", 16); !reflect.DeepEqual(got,
		[]string{"Çok uzun adlı", "bir ürün:", "fırında kaşarlı", "mantar"}) {
		t.Errorf("word split: %q", got)
	}
	got := Indent("  NOT: ", "soğansız, yanında bol yeşillik", 20)
	if want := []string{"  NOT: soğansız,", "       yanında bol", "       yeşillik"}; !reflect.DeepEqual(got, want) {
		t.Errorf("girinti: %q", got)
	}
	for _, l := range Indent("  - ", strings.Repeat("ş", 40), 16) {
		if utf8.RuneCountInString(l) > 16 {
			t.Errorf("overflowing line %q", l)
		}
	}
	if got := Words("", 10); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("empty text: %q", got)
	}
}
