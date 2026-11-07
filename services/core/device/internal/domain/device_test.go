// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
)

func TestPrinterState(t *testing.T) {
	tests := []struct {
		s       PrinterState
		valid   bool
		blocks  bool
		message string
		name    string
	}{
		{PrinterUnknown, true, false, "", "bilinmiyor"},
		{PrinterReady, true, false, "", "hazır"},
		{PrinterPaperNearEnd, true, false, "Kağıt azaldı", "kağıt azaldı"},
		{PrinterPaperOut, true, true, "Kağıt bitti", "kağıt bitti"},
		{PrinterCoverOpen, true, true, "Yazıcının kapağı açık", "kapak açık"},
		{PrinterOffline, true, true, "Yazıcı kapalı ya da bağlı değil", "kapalı"},
		{PrinterError, true, true, "Yazıcı hata veriyor", "hata"},
		{PrinterState(-1), false, false, "", "geçersiz"},
		{PrinterState(99), false, false, "", "geçersiz"},
	}
	for _, tc := range tests {
		if tc.s.Valid() != tc.valid {
			t.Errorf("%d: Valid = %v", tc.s, tc.s.Valid())
		}
		if tc.s.Blocks() != tc.blocks {
			t.Errorf("%d: Blocks = %v", tc.s, tc.s.Blocks())
		}
		if tc.s.Message() != tc.message {
			t.Errorf("%d: Message = %q", tc.s, tc.s.Message())
		}
		if tc.s.String() != tc.name {
			t.Errorf("%d: String = %q", tc.s, tc.s.String())
		}
	}
}

func TestReadingCheck(t *testing.T) {
	tests := []struct {
		name string
		r    Reading
		want error
	}{
		{"sabit", Reading{Grams: 1250, Stable: true}, nil},
		{"sınırda", Reading{Grams: MaxGrams, Stable: true}, nil},
		{"oynuyor", Reading{Grams: 1250}, ErrScaleMotion},
		{"eksi", Reading{Grams: -5, Stable: true}, ErrScaleNegative},
		{"boş", Reading{Stable: true}, ErrScaleEmpty},
		{"kapasite aşıldı", Reading{Grams: MaxGrams + 1, Stable: true}, ErrScaleOver},
	}
	for _, tc := range tests {
		if err := tc.r.Check(); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, istenen %v", tc.name, err, tc.want)
		}
	}
}

func TestValidateScan(t *testing.T) {
	long := make([]byte, MaxScanLen+1)
	for i := range long {
		long[i] = '1'
	}
	tests := []struct {
		code string
		ok   bool
	}{
		{"8690000000012", true},
		{"ABC 123~", true},
		{string(long[:MaxScanLen]), true},
		{"", false},
		{string(long), false},
		{"abc\n", false},
		{"abc\x7f", false},
		{"çay", false},
	}
	for _, tc := range tests {
		err := ValidateScan(tc.code)
		if tc.ok && err != nil {
			t.Errorf("%q reddedildi: %v", tc.code, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidScan) {
			t.Errorf("%q kabul edildi: %v", tc.code, err)
		}
	}
}
