// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package escpos

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// CodePage is the printer's character table: the number selected with ESC t and the
// conversion from UTF-8 to that table. A character with no counterpart is printed as '?';
// the width does not change (the line was checked by column count).
type CodePage struct {
	Name   string
	Number byte
	encode func(rune) (byte, bool)
}

// pc857 is the Turkish DOS code page (IBM 857): most receipt printers have it.
// Only the letters that are certain; the rest '?'.
var pc857 = map[rune]byte{
	'Ç': 0x80, 'ü': 0x81, 'é': 0x82, 'â': 0x83, 'ä': 0x84, 'à': 0x85, 'å': 0x86, 'ç': 0x87,
	'ê': 0x88, 'ë': 0x89, 'è': 0x8A, 'ï': 0x8B, 'î': 0x8C, 'ı': 0x8D, 'Ä': 0x8E, 'Å': 0x8F,
	'É': 0x90, 'æ': 0x91, 'Æ': 0x92, 'ô': 0x93, 'ö': 0x94, 'ò': 0x95, 'û': 0x96, 'ù': 0x97,
	'İ': 0x98, 'Ö': 0x99, 'Ü': 0x9A, 'ø': 0x9B, '£': 0x9C, 'Ø': 0x9D, 'Ş': 0x9E, 'ş': 0x9F,
	'á': 0xA0, 'í': 0xA1, 'ó': 0xA2, 'ú': 0xA3, 'ñ': 0xA4, 'Ñ': 0xA5, 'Ğ': 0xA6, 'ğ': 0xA7,
}

// defaultNumbers are the ESC t numbers (the Epson table; most printers use the same,
// for those that do not, the number is given in the setting: "pc857:61").
var defaultNumbers = map[string]byte{"pc857": 13, "wpc1254": 48}

// ParseCodePage reads a setting of the form "pc857", "pc857:13", "wpc1254" or
// "wpc1254:48".
func ParseCodePage(s string) (CodePage, error) {
	name, num, hasNum := strings.Cut(strings.ToLower(strings.TrimSpace(s)), ":")
	n, ok := defaultNumbers[name]
	if !ok {
		return CodePage{}, fmt.Errorf("kod sayfası %q bilinmiyor (pc857 ya da wpc1254)", name)
	}
	if hasNum {
		v, err := strconv.ParseUint(num, 10, 8)
		if err != nil {
			return CodePage{}, fmt.Errorf("kod sayfası numarası %q geçersiz (0–255)", num)
		}
		n = byte(v)
	}
	cp := CodePage{Name: name, Number: n}
	switch name {
	case "pc857":
		cp.encode = func(r rune) (byte, bool) {
			b, ok := pc857[r]
			return b, ok
		}
	case "wpc1254":
		cp.encode = charmap.Windows1254.EncodeRune
	}
	return cp, nil
}

// Encode converts the line to the code page: ASCII as it is, a character with
// no counterpart '?'. Control characters (which would be commands to the printer)
// are printed as a space.
func (cp CodePage) Encode(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			out = append(out, ' ')
		case r < 0x80:
			out = append(out, byte(r))
		default:
			if b, ok := cp.encode(r); ok {
				out = append(out, b)
			} else {
				out = append(out, '?')
			}
		}
	}
	return out
}
