// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package evdev reads a barcode reader that works like a keyboard over USB from Linux's
// input device (/dev/input/eventN). The reader is set aside for device (EVIOCGRAB): its
// keys do not fall to the register screen (the kiosk browser), the code read goes to the
// basket regardless of where the cursor is.
//
// The reader sends key codes; the US keyboard layout is used to turn them into
// characters (readers come out of the box this way). The code ends with Enter (or Tab).
package evdev

import (
	"time"

	"abarow.com/services/core/device/internal/domain"
)

// Event is an input event (struct input_event).
type Event struct {
	Time  time.Duration // since boot; used only for the gap
	Type  uint16
	Code  uint16
	Value int32
}

const (
	evKey = 1

	keyTab        = 15
	keyEnter      = 28
	keyLeftShift  = 42
	keyRightShift = 54
	keyKPEnter    = 96

	// maxGap is the longest time between two keys of a code. The reader types a code
	// within milliseconds; a longer gap is an interrupted read (e.g. the reader was
	// unplugged) and is discarded.
	maxGap = 500 * time.Millisecond
)

// keys are the characters of the key codes in the US keyboard layout: [code] = {plain,
// shifted}. The shifted form of a letter is the capital letter.
var keys = map[uint16][2]byte{
	2: {'1', '!'}, 3: {'2', '@'}, 4: {'3', '#'}, 5: {'4', '$'}, 6: {'5', '%'},
	7: {'6', '^'}, 8: {'7', '&'}, 9: {'8', '*'}, 10: {'9', '('}, 11: {'0', ')'},
	12: {'-', '_'}, 13: {'=', '+'},
	16: {'q', 'Q'}, 17: {'w', 'W'}, 18: {'e', 'E'}, 19: {'r', 'R'}, 20: {'t', 'T'},
	21: {'y', 'Y'}, 22: {'u', 'U'}, 23: {'i', 'I'}, 24: {'o', 'O'}, 25: {'p', 'P'},
	26: {'[', '{'}, 27: {']', '}'},
	30: {'a', 'A'}, 31: {'s', 'S'}, 32: {'d', 'D'}, 33: {'f', 'F'}, 34: {'g', 'G'},
	35: {'h', 'H'}, 36: {'j', 'J'}, 37: {'k', 'K'}, 38: {'l', 'L'},
	39: {';', ':'}, 40: {'\'', '"'}, 41: {'`', '~'}, 43: {'\\', '|'},
	44: {'z', 'Z'}, 45: {'x', 'X'}, 46: {'c', 'C'}, 47: {'v', 'V'}, 48: {'b', 'B'},
	49: {'n', 'N'}, 50: {'m', 'M'}, 51: {',', '<'}, 52: {'.', '>'}, 53: {'/', '?'},
	57: {' ', ' '},
	// Numeric keypad.
	55: {'*', '*'}, 71: {'7', '7'}, 72: {'8', '8'}, 73: {'9', '9'}, 74: {'-', '-'},
	75: {'4', '4'}, 76: {'5', '5'}, 77: {'6', '6'}, 78: {'+', '+'}, 79: {'1', '1'},
	80: {'2', '2'}, 81: {'3', '3'}, 82: {'0', '0'}, 83: {'.', '.'}, 98: {'/', '/'},
}

// Decoder turns key events into the codes read.
type Decoder struct {
	buf   []byte
	shift bool
	// broken: the read is corrupt (an unknown key, a code too long): the keys coming until the
	// code ends are discarded, a half code does not go to the screen.
	broken bool
	last   time.Duration
}

// Feed processes an event; if a code is finished it returns it.
func (d *Decoder) Feed(ev Event) (string, bool) {
	if ev.Type != evKey {
		return "", false
	}
	if ev.Code == keyLeftShift || ev.Code == keyRightShift {
		d.shift = ev.Value != 0
		return "", false
	}
	if ev.Value != 1 { // only the press; release is 0, auto-repeat 2
		return "", false
	}
	if (len(d.buf) > 0 || d.broken) && ev.Time-d.last > maxGap {
		d.reset()
	}
	d.last = ev.Time
	switch ev.Code {
	case keyEnter, keyKPEnter, keyTab:
		code, ok := string(d.buf), len(d.buf) > 0 && !d.broken
		d.reset()
		return code, ok
	}
	c, known := keys[ev.Code]
	switch {
	case !known, len(d.buf) >= domain.MaxScanLen:
		d.broken = true
	case !d.broken && d.shift:
		d.buf = append(d.buf, c[1])
	case !d.broken:
		d.buf = append(d.buf, c[0])
	}
	return "", false
}

func (d *Decoder) reset() {
	d.buf, d.broken = d.buf[:0], false
}
