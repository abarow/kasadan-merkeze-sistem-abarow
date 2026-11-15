// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package layout lays out fixed-width printer lines (receipt, report).
// The width is a count of characters (runes), not bytes; no line exceeds the
// width, text that does not fit is split.
package layout

import (
	"strings"
	"unicode/utf8"
)

// Rule is a line across the width.
func Rule(width int) string { return strings.Repeat("-", width) }

// Wrap splits the text into pieces that do not exceed the width.
func Wrap(s string, width int) []string {
	r := []rune(s)
	var out []string
	for len(r) > width {
		out = append(out, string(r[:width]))
		r = r[width:]
	}
	return append(out, string(r))
}

// Center centers the text; if it does not fit it centers the split pieces separately.
func Center(s string, width int) []string {
	parts := Wrap(s, width)
	for i, p := range parts {
		pad := (width - utf8.RuneCountInString(p)) / 2
		parts[i] = strings.Repeat(" ", pad) + p
	}
	return parts
}

// Pair aligns the left to the left and the right to the right. If it does not fit on a single line with at least one
// space between, the left goes on its own line(s) and the right right-aligned on a sub-line.
func Pair(left, right string, width int) []string {
	lw, rw := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	if lw+1+rw <= width {
		return []string{left + strings.Repeat(" ", width-lw-rw) + right}
	}
	out := Wrap(left, width)
	for _, r := range Wrap(right, width) {
		out = append(out, strings.Repeat(" ", width-utf8.RuneCountInString(r))+r)
	}
	return out
}

// Words splits the text word by word into lines that do not exceed the width; a word that
// does not fit on its own is split.
func Words(s string, width int) []string { return Indent("", s, width) }

// Indent starts the first line with prefix and aligns the following ones with spaces of the same width
// (e.g. a note starting with "  NOT: "): it splits word by word.
// If the width is not enough for the prefix it splits without the prefix.
func Indent(prefix, s string, width int) []string {
	lead := strings.Repeat(" ", utf8.RuneCountInString(prefix))
	room := width - utf8.RuneCountInString(prefix)
	if room < 8 {
		prefix, lead, room = "", "", width
	}
	var out []string
	line := ""
	flush := func() {
		p := lead
		if len(out) == 0 {
			p = prefix
		}
		out = append(out, p+line)
		line = ""
	}
	for _, word := range strings.Fields(s) {
		for utf8.RuneCountInString(word) > room {
			if line != "" {
				flush()
			}
			r := []rune(word)
			line = string(r[:room])
			flush()
			word = string(r[room:])
		}
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= room:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	if line != "" || len(out) == 0 {
		flush()
	}
	return out
}
