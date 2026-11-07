// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"fmt"
)

// MaxScanLen is the longest a code read can be (the forms of GS1-128 and QR that will
// come to the screen are below this).
const MaxScanLen = 128

// ErrInvalidScan means the code read is invalid.
var ErrInvalidScan = fmt.Errorf("barkod 1–%d yazdırılabilir ASCII karakter olmalı", MaxScanLen)

// ErrNoSimulator is asking for a scan imitation on a real reader.
var ErrNoSimulator = errors.New("barkod okuyucu simülatör değil; okutma taklit edilemez")

// ValidateScan checks that the code can be passed to the screen: not empty, at most
// MaxScanLen, printable ASCII only (the reader types like a US keyboard; no other
// character comes, and if one does it is a faulty read).
func ValidateScan(code string) error {
	if code == "" || len(code) > MaxScanLen {
		return ErrInvalidScan
	}
	for i := 0; i < len(code); i++ {
		if code[i] < 0x20 || code[i] > 0x7e {
			return ErrInvalidScan
		}
	}
	return nil
}
