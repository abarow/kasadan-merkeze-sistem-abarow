// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/money"
)

func TestShiftClose(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	ayse := Cashier{ID: "ayse", Name: "Ayşe", Role: RoleCashier}
	mert := Cashier{ID: "mert", Role: RoleCashier}
	ali := Cashier{ID: "ali", Role: RoleManager}

	if _, err := NewShift(ayse, -1, at); !errors.Is(err, ErrNegativeCash) {
		t.Errorf("negative opening cash: %v", err)
	}
	s, err := NewShift(ayse, 50000, at)
	if err != nil || !s.Open() || s.CashierName != "Ayşe" {
		t.Fatalf("shift was not opened: %+v, %v", s, err)
	}
	if err := s.Close(mert, 0, at); !errors.Is(err, ErrNotShiftOwner) {
		t.Errorf("another cashier could close: %v", err)
	}
	if err := s.Close(ayse, -5, at); !errors.Is(err, ErrNegativeCash) {
		t.Errorf("a negative count was accepted: %v", err)
	}
	if !s.Open() {
		t.Fatal("the rejected closing closed the shift")
	}
	// A manager can close someone else's shift.
	if err := s.Close(ali, 60000, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s.Open() || s.ClosedBy != "ali" || s.CountedCash != 60000 {
		t.Errorf("closing is wrong: %+v", s)
	}
	if err := s.Close(ali, 0, at); !errors.Is(err, ErrNoShift) {
		t.Errorf("a closed shift closed a second time: %v", err)
	}
}

func TestBuildShiftReport(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	s := Shift{ID: 3, CashierID: "ayse", OpenedAt: at, OpeningCash: 50000}
	completed := []CompletedSale{
		// Cash 200.00, change 74.63: 125.37 goes into the drawer.
		{SaleID: "s1", ReceiptNo: 1, Total: 12537, Change: 7463, Payments: []PaymentTotal{{Method: Cash, Amount: 20000}}},
		// Nakit 29,00 + kart 20,00.
		{SaleID: "s2", ReceiptNo: 2, Total: 4900, Payments: []PaymentTotal{{Method: Cash, Amount: 2900}, {Method: Card, Amount: 2000}}},
	}
	voided := []VoidedSale{{SaleID: "s3", Total: 1299}}

	// 100.00 of small change was put in the register, the manager took out 200.00 of interim cash.
	movements := []CashMovement{{Kind: CashIn, Amount: 10000}, {Kind: CashOut, Amount: 20000}}

	opens := []DrawerOpen{{Reason: "Bozuk para"}}

	if _, err := BuildShiftReport(s, completed, voided, movements, opens); !errors.Is(err, ErrShiftNotClosed) {
		t.Fatalf("the report of an open shift came out (blind count is broken): %v", err)
	}
	// Expected: 500.00 + 125.37 + 29.00 + 100.00 − 200.00 = 554.37.
	// Counted 553.37: 1.00 short.
	if err := s.Close(Cashier{ID: "ayse"}, 55337, at.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r, err := BuildShiftReport(s, completed, voided, movements, opens)
	if err != nil {
		t.Fatal(err)
	}
	if r.ReceiptCount != 2 || r.Total != 17437 || r.VoidedCount != 1 || r.DrawerOpens != 1 {
		t.Errorf("totals are wrong: %+v", r.Totals)
	}
	if r.CashIn != 10000 || r.CashOut != 20000 || r.ExpectedCash != 55437 || r.Difference != -100 {
		t.Errorf("put in %d, taken out %d, expected %d, difference %d; want 10000, 20000, 55437, -100",
			r.CashIn, r.CashOut, r.ExpectedCash, r.Difference)
	}
}

func TestRecordCashMovement(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	ayse := Cashier{ID: "ayse", Role: RoleCashier}
	mert := Cashier{ID: "mert", Role: RoleCashier}
	ali := Cashier{ID: "ali", Role: RoleManager}
	s := Shift{ID: 3, CashierID: "ayse", OpenedAt: at}

	tests := []struct {
		name   string
		by     Cashier
		kind   CashMovementKind
		amount int64
		note   string
		want   error
	}{
		{"sahibi para koyar", ayse, CashIn, 10000, "Bozuk para", nil},
		{"müdür para koyar", ali, CashIn, 10000, "Bozuk para", nil},
		{"başka kasiyer para koyamaz", mert, CashIn, 10000, "Bozuk para", ErrNotShiftOwner},
		{"müdür para alır", ali, CashOut, 20000, "Ara para", nil},
		{"sahibi para alamaz", ayse, CashOut, 20000, "Ara para", ErrCashOutNotManager},
		{"sıfır tutar", ali, CashIn, 0, "Bozuk para", ErrCashAmount},
		{"eksi tutar", ali, CashOut, -100, "Ara para", ErrCashAmount},
		{"boş açıklama", ali, CashIn, 100, "   ", ErrCashNote},
		{"uzun açıklama", ali, CashIn, 100, strings.Repeat("ş", 201), ErrCashNote},
		{"bilinmeyen hareket", ali, "x", 100, "?", ErrCashKind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := s.RecordCashMovement(tt.by, tt.kind, money.Money(tt.amount), tt.note, at)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error %v, want %v", err, tt.want)
			}
			if err == nil && (m.ShiftID != 3 || m.By != tt.by.ID || m.Note != strings.TrimSpace(tt.note) || !m.At.Equal(at)) {
				t.Errorf("movement is wrong: %+v", m)
			}
		})
	}

	closed := s
	closed.ClosedAt = at.Add(time.Hour)
	if _, err := closed.RecordCashMovement(ali, CashIn, 100, "Bozuk para", at); !errors.Is(err, ErrNoShift) {
		t.Errorf("movement to a closed shift: %v", err)
	}
}

func TestRecordDrawerOpen(t *testing.T) {
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s := Shift{ID: 3, CashierID: "ayse", OpenedAt: at}
	ali := Cashier{ID: "ali", Role: RoleManager}
	if _, err := s.RecordDrawerOpen(Cashier{ID: "ayse", Role: RoleCashier}, "ayse", "Bozuk para", at); !errors.Is(err, ErrDrawerNotManager) {
		t.Errorf("a cashier could open: %v", err)
	}
	if _, err := s.RecordDrawerOpen(ali, "ayse", "  ", at); !errors.Is(err, ErrDrawerReason) {
		t.Errorf("opening without a reason: %v", err)
	}
	d, err := s.RecordDrawerOpen(ali, "ayse", " Bozuk para ", at)
	if err != nil || d.ShiftID != 3 || d.By != "ali" || d.RequestedBy != "ayse" || d.Reason != "Bozuk para" {
		t.Errorf("opening: %+v, %v", d, err)
	}
	s.ClosedAt = at
	if _, err := s.RecordDrawerOpen(ali, "ali", "Bozuk para", at); !errors.Is(err, ErrNoShift) {
		t.Errorf("opening in a closed shift: %v", err)
	}
}
