// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"testing"
	"time"

	"abarow.com/pkg/ledger"
	pkgmoney "abarow.com/pkg/money"
	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/store/internal/domain"
)

func money(kurus int64) pkgmoney.Money { return pkgmoney.Money(kurus) }

func sale(id, register string, receiptNo int64, at time.Time) domain.Sale {
	return domain.Sale{
		ID: id, StoreID: "store-1", RegisterID: register, CashierID: "ayse", ReceiptNo: receiptNo, FiscalNo: "SIM-1",
		Total: 2500, TaxTotal: 25, Change: 500, PaidAt: at,
		Lines: []domain.Line{
			{ProductID: "p3", Barcode: "869", Name: "EKMEK", Qty: 1000, UnitPrice: 1200, TaxRate: 100},
			{ProductID: "p2", Name: "Domates", Qty: 1250, UnitPrice: 1040, TaxRate: 100},
		},
		Payments: []domain.Payment{{Method: domain.Cash, Amount: 2000}, {Method: domain.Card, Amount: 1000}},
		Taxes:    []domain.Tax{{Rate: 100, Gross: 2500, Tax: 25}},
	}
}

func TestSaveAndReport(t *testing.T) {
	ctx := context.Background()
	s := postgrestest.Open(t)
	day := time.Date(2026, 10, 25, 21, 0, 0, 0, time.UTC) // Istanbul 26.10 00:00
	in := sale("s1", "register-1", 1, day.Add(time.Minute))
	for range 2 { // olay iki kez gelir
		if err := s.SaveSale(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveSale(ctx, sale("s0", "register-1", 2, day.Add(-time.Nanosecond))); err != nil { // the day before
		t.Fatal(err)
	}
	if err := s.SaveSale(ctx, sale("s2", "register-1", 3, day.Add(24*time.Hour))); err != nil { // the next day
		t.Fatal(err)
	}
	v := domain.Void{SaleID: "v1", StoreID: "store-1", RegisterID: "register-2", CashierID: "mert", Total: 999, Refunded: true, At: day.Add(time.Hour)}
	for range 2 {
		if err := s.SaveVoid(ctx, v); err != nil {
			t.Fatal(err)
		}
	}

	ist, _ := time.LoadLocation("Europe/Istanbul")
	r, err := s.Report(ctx, ledger.Filter{Start: day, End: day.Add(24 * time.Hour), Location: ist})
	if err != nil {
		t.Fatal(err)
	}
	// One sale and one cancellation during the day; the sales of the previous and next day
	// do not count. The change is deducted from cash.
	if r.Receipts != 1 || r.Total != 2500 || r.TaxTotal != 25 || r.Voided != 1 || r.VoidedTotal != 999 {
		t.Errorf("toplamlar %+v", r)
	}
	if len(r.Payments) != 2 || r.Payments[0] != (ledger.PaymentTotal{Method: domain.Cash, Amount: 1500}) ||
		r.Payments[1] != (ledger.PaymentTotal{Method: domain.Card, Amount: 1000}) {
		t.Errorf("payments %+v", r.Payments)
	}
	if len(r.Taxes) != 1 || r.Taxes[0] != in.Taxes[0] {
		t.Errorf("KDV %+v", r.Taxes)
	}
	if len(r.Products) != 2 || r.Products[0] != (ledger.ProductTotal{ProductID: "p2", Name: "Domates", Qty: 1250, Total: 1300}) ||
		r.Products[1] != (ledger.ProductTotal{ProductID: "p3", Name: "EKMEK", Qty: 1000, Total: 1200}) {
		t.Errorf("products %+v", r.Products)
	}
	if len(r.Registers) != 2 || r.Registers[0] != (ledger.GroupTotal{StoreID: "store-1", ID: "register-1", Receipts: 1, Total: 2500}) ||
		r.Registers[1] != (ledger.GroupTotal{StoreID: "store-1", ID: "register-2", Voided: 1, VoidedTotal: 999}) {
		t.Errorf("kasalar %+v", r.Registers)
	}
	if r.Hours[0].Receipts != 1 || len(r.Days) != 1 || r.Days[0].Date != "2026-10-26" {
		t.Errorf("hours and days: %+v %+v", r.Hours[0], r.Days)
	}
}

func TestActivity(t *testing.T) {
	ctx := context.Background()
	s := postgrestest.Open(t)
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	// register-1: 4, 5, 7, 10 → missing 6 and 8–9; register-2: 1, 2 → none missing.
	for i, n := range []int64{10, 4, 7, 5} {
		if err := s.SaveSale(ctx, sale("k1-"+string(rune('a'+i)), "register-1", n, at.Add(time.Duration(n)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []int64{1, 2} {
		if err := s.SaveSale(ctx, sale("k2-"+string(rune('0'+n)), "register-2", n, at)); err != nil {
			t.Fatal(err)
		}
	}
	act, err := s.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k1 := act["register-1"]
	if k1.Receipts != 4 || k1.LastReceiptNo != 10 || !k1.LastSaleAt.Equal(at.Add(10*time.Minute)) {
		t.Errorf("register-1: %+v", k1)
	}
	if len(k1.Missing) != 2 || k1.Missing[0] != (domain.ReceiptRange{From: 6, To: 6}) || k1.Missing[1] != (domain.ReceiptRange{From: 8, To: 9}) {
		t.Errorf("register-1 missing receipts: %+v", k1.Missing)
	}
	if k2 := act["register-2"]; k2.Receipts != 2 || len(k2.Missing) != 0 {
		t.Errorf("register-2: %+v", k2)
	}
	if _, ok := act["register-3"]; ok {
		t.Error("a register without sales is in the list")
	}
}

func TestShifts(t *testing.T) {
	ctx := context.Background()
	s := postgrestest.Open(t)
	at := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	opened := func(reg string, id int64, cashier string, h int) domain.Shift {
		return domain.Shift{RegisterID: reg, ID: id, CashierID: cashier, CashierName: "Kasiyer " + cashier, OpenedAt: at.Add(time.Duration(h) * time.Hour), OpeningCash: 50000}
	}
	closed := func(sh domain.Shift, h int, diff int64) domain.Shift {
		sh.ClosedAt, sh.ClosedBy = at.Add(time.Duration(h)*time.Hour), sh.CashierID
		sh.Receipts, sh.Total, sh.ExpectedCash, sh.CountedCash, sh.Difference = 12, 12345, 60000, 60000+money(diff), money(diff)
		return sh
	}
	// register-1: shift 1 opened and closed (cash shortage 1.00), shift 2 open.
	// The result does not change even if the event comes twice.
	for range 2 {
		for _, sh := range []domain.Shift{opened("register-1", 1, "ayse", 0), closed(opened("register-1", 1, "ayse", 0), 8, -100), opened("register-1", 2, "mert", 8)} {
			if err := s.SaveShift(ctx, sh); err != nil {
				t.Fatal(err)
			}
		}
	}
	// If the opening event comes again after the closing, the closing is not deleted.
	if err := s.SaveShift(ctx, opened("register-1", 1, "ayse", 0)); err != nil {
		t.Fatal(err)
	}
	// register-2: shift 7 closed; the closing of shift 5 never came (old).
	for _, sh := range []domain.Shift{opened("register-2", 5, "ali", -24), opened("register-2", 7, "ali", 0), closed(opened("register-2", 7, "ali", 0), 4, 0)} {
		if err := s.SaveShift(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	act, err := s.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k1 := act["register-1"]
	if k1.OpenShift == nil || k1.OpenShift.ID != 2 || k1.OpenShift.CashierName != "Kasiyer mert" || !k1.OpenShift.Open() {
		t.Errorf("register-1 open shift: %+v", k1.OpenShift)
	}
	if lc := k1.LastClosed; lc == nil || lc.ID != 1 || lc.Difference != -100 || lc.ExpectedCash != 60000 || lc.CountedCash != 59900 ||
		lc.Receipts != 12 || lc.ClosedBy != "ayse" || !lc.ClosedAt.Equal(at.Add(8*time.Hour)) {
		t.Errorf("register-1 last closed: %+v", lc)
	}
	k2 := act["register-2"]
	if k2.OpenShift != nil || k2.LastClosed == nil || k2.LastClosed.ID != 7 {
		t.Errorf("register-2: open %+v, last closed %+v", k2.OpenShift, k2.LastClosed)
	}
}

func TestUnfinished(t *testing.T) {
	ctx := context.Background()
	s := postgrestest.Open(t)
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	u := ledger.Unfinished{SaleID: "c1", StoreID: "store-1", RegisterID: "register-1", CashierID: "ayse", Total: 12537, Reason: "kağıt yok", At: at}
	if err := s.SaveUnfinished(ctx, u); err != nil {
		t.Fatal(err)
	}
	// A new failed attempt: the first time is kept, the reason and the last attempt are updated.
	retry := u
	retry.Reason, retry.At = "yazıcı yanıt vermiyor", at.Add(time.Minute)
	if err := s.SaveUnfinished(ctx, retry); err != nil {
		t.Fatal(err)
	}
	// Eski deneme sonradan gelirse yeniyi bozmaz.
	if err := s.SaveUnfinished(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c2", "c3"} {
		if err := s.SaveUnfinished(ctx, ledger.Unfinished{SaleID: id, StoreID: "store-1", RegisterID: "register-2", Total: 100, Reason: "x", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	act, err := s.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := act["register-1"].Unfinished
	if len(got) != 1 || got[0].SaleID != "c1" || !got[0].Since.Equal(at) || !got[0].LastAttempt.Equal(at.Add(time.Minute)) ||
		got[0].Reason != "yazıcı yanıt vermiyor" || got[0].Total != 12537 || got[0].CashierID != "ayse" {
		t.Fatalf("register-1 pending: %+v", got)
	}
	if len(act["register-2"].Unfinished) != 2 {
		t.Fatalf("register-2 pending: %+v", act["register-2"].Unfinished)
	}

	// One is completed (receipt issued), one has its payment refunded and is cancelled.
	if err := s.SaveSale(ctx, sale("c2", "register-2", 1, at)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVoid(ctx, domain.Void{SaleID: "c3", StoreID: "store-1", RegisterID: "register-2", Total: 100, Refunded: true, At: at}); err != nil {
		t.Fatal(err)
	}
	if act, err = s.Activity(ctx); err != nil {
		t.Fatal(err)
	}
	if len(act["register-2"].Unfinished) != 0 || len(act["register-1"].Unfinished) != 1 {
		t.Errorf("a completed and refunded sale is still pending: %+v", act["register-2"].Unfinished)
	}
}
