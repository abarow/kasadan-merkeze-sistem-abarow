// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

// PrinterState is the printer's state. The order matters: if there is more than one problem
// the most severe is reported (with the cover open the paper may look out too).
type PrinterState int

const (
	// PrinterUnknown: the printer does not report its state or it has not been asked yet.
	PrinterUnknown PrinterState = iota
	PrinterReady
	PrinterPaperNearEnd
	PrinterPaperOut
	PrinterCoverOpen
	PrinterOffline
	PrinterError
)

var (
	// ErrInvalidPrinterState is an unknown state (given to the simulator).
	ErrInvalidPrinterState = errors.New("yazıcı durumu geçersiz")
	// ErrNoPrinterSimulator is asking for a state imitation on a real printer.
	ErrNoPrinterSimulator = errors.New("yazıcı simülatör değil; durumu ayarlanamaz")
)

// Message is the explanation to show the cashier; empty when ready and when unknown.
func (s PrinterState) Message() string {
	switch s {
	case PrinterPaperNearEnd:
		return "Kağıt azaldı"
	case PrinterPaperOut:
		return "Kağıt bitti"
	case PrinterCoverOpen:
		return "Yazıcının kapağı açık"
	case PrinterOffline:
		return "Yazıcı kapalı ya da bağlı değil"
	case PrinterError:
		return "Yazıcı hata veriyor"
	}
	return ""
}

// Blocks: the printer cannot print in this state.
func (s PrinterState) Blocks() bool {
	switch s {
	case PrinterPaperOut, PrinterCoverOpen, PrinterOffline, PrinterError:
		return true
	}
	return false
}

// Valid: the state is defined.
func (s PrinterState) Valid() bool { return s >= PrinterUnknown && s <= PrinterError }

// String is the state's name in the logs.
func (s PrinterState) String() string {
	names := [...]string{"bilinmiyor", "hazır", "kağıt azaldı", "kağıt bitti", "kapak açık", "kapalı", "hata"}
	if !s.Valid() {
		return "geçersiz"
	}
	return names[s]
}
