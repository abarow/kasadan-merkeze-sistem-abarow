// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"slices"
	"strconv"

	"abarow.com/pkg/money"
)

// ScalePrefixes are the prefixes of scale barcodes. In GS1 20-29 are reserved
// for in-store use; which of them are used for the scale may differ
// according to the scale's settings.
var ScalePrefixes = []string{"27", "28", "29"}

// ParseScaleBarcode decodes the EAN-13 barcode the scale prints: a 2-digit prefix,
// a 5-digit product code, a 5-digit weight (grams), the check digit.
//
//	2700001012504 → catalog key "2700001", 1.250 kg
//
// If the barcode is not a scale barcode it returns ok false. Since a gram is a thousandth
// of a kilogram the weight is directly a money.Qty.
func ParseScaleBarcode(code string) (key string, qty money.Qty, ok bool, err error) {
	if len(code) != 13 || !slices.Contains(ScalePrefixes, code[:2]) {
		return "", 0, false, nil
	}
	if !isDigits(code) || CheckDigit(code[:12]) != code[12] {
		return "", 0, true, ErrInvalidScale
	}
	grams, _ := strconv.Atoi(code[7:12])
	if grams == 0 {
		return "", 0, true, ErrInvalidScale
	}
	return code[:7], money.Qty(grams), true, nil
}

// CheckDigit, EAN-13 barkodun ilk 12 hanesinden kontrol hanesini hesaplar.
func CheckDigit(first12 string) byte {
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(first12[i] - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
