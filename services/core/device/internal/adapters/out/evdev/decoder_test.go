// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package evdev

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// typing is the reader typing a code: a press and release for each key, shift if the letter
// is capital; 2 ms apart (readers are this fast).
type typing struct {
	t      time.Duration
	events []Event
}

func (ty *typing) key(code uint16, value int32) {
	ty.t += 2 * time.Millisecond
	ty.events = append(ty.events, Event{Time: ty.t, Type: evKey, Code: code, Value: value})
	ty.events = append(ty.events, Event{Time: ty.t, Type: 0}) // EV_SYN
}

func (ty *typing) press(code uint16) {
	ty.key(code, 1)
	ty.key(code, 0)
}

func (ty *typing) text(s string) {
	for _, r := range s {
		code, shift := lookup(byte(r))
		if shift {
			ty.key(keyLeftShift, 1)
		}
		ty.press(code)
		if shift {
			ty.key(keyLeftShift, 0)
		}
	}
}

// lookup finds the character's key in the US layout (not the numeric keypad).
func lookup(c byte) (uint16, bool) {
	for code, k := range keys {
		if code >= 55 && code != 57 { // except the numeric keypad and *
			continue
		}
		if k[0] == c {
			return code, false
		}
		if k[1] == c {
			return code, true
		}
	}
	panic("tuş yok: " + string(c))
}

func decode(events []Event) []string {
	var d Decoder
	var out []string
	for _, ev := range events {
		if code, ok := d.Feed(ev); ok {
			out = append(out, code)
		}
	}
	return out
}

func TestDecoder(t *testing.T) {
	var ty typing
	ty.text("8690000000012")
	ty.press(keyEnter)
	// Letters, shift and signs (Code 128).
	ty.text("Abc-12/X+")
	ty.press(keyKPEnter)
	// Sonu Tab olan okuyucu.
	ty.text("2990000000994")
	ty.press(keyTab)
	// An empty Enter is not a code.
	ty.press(keyEnter)
	if got := decode(ty.events); !slices.Equal(got, []string{"8690000000012", "Abc-12/X+", "2990000000994"}) {
		t.Errorf("kodlar %q", got)
	}

	// The numeric keypad and auto-repeat (value 2) are ignored.
	ty = typing{}
	for _, k := range []uint16{79, 80, 81} { // 1 2 3
		ty.press(k)
	}
	ty.key(81, 2)
	ty.press(keyEnter)
	if got := decode(ty.events); !slices.Equal(got, []string{"123"}) {
		t.Errorf("numeric set %q", got)
	}
}

// A corrupt or half read does not go to the screen; the next read is not affected.
func TestDecoderBroken(t *testing.T) {
	var ty typing
	// Unknown key (F1 = 59): the code is discarded.
	ty.text("12")
	ty.press(59)
	ty.text("34")
	ty.press(keyEnter)
	// An interrupted read (the reader was unplugged): a new code after a long gap.
	ty.text("999")
	ty.t += time.Second
	ty.text("5555")
	ty.press(keyEnter)
	// A code that is too long is discarded.
	ty.text(strings.Repeat("7", 129))
	ty.press(keyEnter)
	ty.text("42")
	ty.press(keyEnter)
	if got := decode(ty.events); !slices.Equal(got, []string{"5555", "42"}) {
		t.Errorf("kodlar %q", got)
	}
}
