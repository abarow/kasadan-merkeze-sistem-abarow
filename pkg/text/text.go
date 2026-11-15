// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package text holds the search and sort keys of Turkish text. The register,
// store server and headquarters use the same key: the same search finds the same
// products in every tier, the same sort gives the same order.
package text

import (
	"strings"
	"unicode"
)

// alphabet is the order of the Turkish alphabet; q, w and x are put in their
// places for foreign words.
const alphabet = "abcçdefgğhıijklmnoöpqrsştuüvwxyz"

// order is the letter's sort byte: increasing values starting from 'A' (0x41).
// Digits (0x30-0x39) and space come before letters.
var order = func() map[rune]byte {
	m := map[rune]byte{}
	for i, r := range []rune(alphabet) {
		m[r] = byte('A' + i)
	}
	// Letters with a circumflex are in the same order as their plain forms.
	m['â'], m['î'], m['û'] = m['a'], m['i'], m['u']
	return m
}()

// SortKey is the key that sorts the name by the Turkish alphabet: the byte order
// of the keys is the alphabetical order of the names (Ç after C and before D; ı
// before i). Upper/lower case is not distinguished. Other characters outside ASCII
// come after the letters.
func SortKey(name string) string {
	var b strings.Builder
	for _, r := range Fold(name) {
		switch c, ok := order[r]; {
		case ok:
			b.WriteByte(c)
		case r < 0x80:
			b.WriteRune(r)
		default:
			b.WriteByte(0x7f)
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Fold lower-cases the text with the Turkish rules; search is done with this form:
// "ŞAMPUAN" → "şampuan", "IŞIK" → "ışık".
func Fold(s string) string {
	return strings.ToLowerSpecial(unicode.TurkishCase, strings.TrimSpace(s))
}
