// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
	"abarow.com/services/core/sales/internal/domain"
)

var now = time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)

// testLines: 1 unit of 12.99 TL with 20% VAT + 1.250 kg of 89.90 TL/kg with 1% VAT.
// Total 12,537 kuruş; VAT 1% → 111, 20% → 217.
func testLines() []domain.Line {
	return []domain.Line{
		{ProductID: "p1", Barcode: "8690000000011", Name: "Deterjan 1 L", Qty: money.Units(1), UnitPrice: 1299, TaxRate: 2000},
		{ProductID: "p2", Barcode: "2700001012500", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100},
	}
}

func newSale(t *testing.T) *domain.Sale {
	t.Helper()
	s, err := domain.NewSale("s1", "k1", "", testLines(), now)
	if err != nil {
		t.Fatalf("NewSale: %v", err)
	}
	return s
}

func cash(a money.Money) domain.Payment { return domain.Payment{Method: domain.PaymentCash, Amount: a} }
func card(a money.Money) domain.Payment { return domain.Payment{Method: domain.PaymentCard, Amount: a} }
func meal(a money.Money) domain.Payment {
	return domain.Payment{Method: domain.PaymentMealCard, Amount: a}
}

func with(l domain.Line, change func(*domain.Line)) domain.Line {
	change(&l)
	return l
}

func TestNewSaleValidation(t *testing.T) {
	valid := testLines()[0]
	tests := []struct {
		name  string
		lines []domain.Line
		want  error
	}{
		{"satırsız", nil, domain.ErrNoLines},
		{"ürün kimliği boş", []domain.Line{with(valid, func(l *domain.Line) { l.ProductID = "" })}, domain.ErrInvalidLine},
		{"ürün adı boş", []domain.Line{with(valid, func(l *domain.Line) { l.Name = "" })}, domain.ErrInvalidLine},
		{"sıfır miktar", []domain.Line{with(valid, func(l *domain.Line) { l.Qty = 0 })}, domain.ErrInvalidLine},
		{"negatif miktar", []domain.Line{with(valid, func(l *domain.Line) { l.Qty = -1000 })}, domain.ErrInvalidLine},
		{"negatif fiyat", []domain.Line{with(valid, func(l *domain.Line) { l.UnitPrice = -1 })}, domain.ErrInvalidLine},
		{"negatif KDV", []domain.Line{with(valid, func(l *domain.Line) { l.TaxRate = -1 })}, domain.ErrInvalidLine},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := domain.NewSale("s1", "k1", "", tt.lines, now); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewSaleRequiresIDs(t *testing.T) {
	if _, err := domain.NewSale("", "k1", "", testLines(), now); err == nil {
		t.Error("an empty sale id was accepted")
	}
	if _, err := domain.NewSale("s1", "", "", testLines(), now); err == nil {
		t.Error("an empty register id was accepted")
	}
}

func TestSaleTotals(t *testing.T) {
	s := newSale(t)
	if got := s.Total(); got != 12537 {
		t.Errorf("Total = %d, want 12537", got)
	}
	wantTaxes := []domain.TaxLine{
		{Rate: 100, Gross: 11238, Tax: 111},
		{Rate: 2000, Gross: 1299, Tax: 217},
	}
	if got := s.TaxBreakdown(); !reflect.DeepEqual(got, wantTaxes) {
		t.Errorf("TaxBreakdown = %+v, want %+v", got, wantTaxes)
	}
	if got := s.TaxTotal(); got != 328 {
		t.Errorf("TaxTotal = %d, want 328", got)
	}
	if got := s.Change(); got != 0 {
		t.Errorf("Change = %d on an unpaid sale, want 0", got)
	}
}

// TestGIBReceiptExample reproduces the example receipt in GİB's YN ÖKC receipt format
// guide (Technical Guide Draft Version 1.0, section 2.1):
// EKMEK 1% 12.00 + AYAKKABI 10% 22.00 + ŞAMPUAN 20% 15.00 →
// TOPKDV 4.62, TOPLAM 49.00.
func TestGIBReceiptExample(t *testing.T) {
	lines := []domain.Line{
		{ProductID: "1", Name: "EKMEK", Qty: money.Units(1), UnitPrice: 1200, TaxRate: 100},
		{ProductID: "2", Name: "AYAKKABI", Qty: money.Units(1), UnitPrice: 2200, TaxRate: 1000},
		{ProductID: "3", Name: "ŞAMPUAN", Qty: money.Units(1), UnitPrice: 1500, TaxRate: 2000},
	}
	s, err := domain.NewSale("s1", "k1", "", lines, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.TaxTotal(); got != 462 {
		t.Errorf("TOPKDV = %s, want 4,62", got)
	}
	if got := s.Total(); got != 4900 {
		t.Errorf("TOPLAM = %s, want 49,00", got)
	}
}

func TestTaxBreakdownUsesRateTotals(t *testing.T) {
	// Three 0.10 TL products with 20% VAT: computed line by line each would be
	// 1.67 → 2 kuruş, 6 kuruş in total. Computed over the rate total (0.30 TL)
	// it gives 5 kuruş.
	line := domain.Line{ProductID: "p", Name: "Sakız", Qty: money.Units(1), UnitPrice: 10, TaxRate: 2000}
	s, err := domain.NewSale("s1", "k1", "", []domain.Line{line, line, line}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.TaxTotal(); got != 5 {
		t.Errorf("TaxTotal = %d, want 5", got)
	}
}

func TestCheckPayments(t *testing.T) {
	tests := []struct {
		name     string
		payments []domain.Payment
		want     error
	}{
		{"tam kart", []domain.Payment{card(12537)}, nil},
		{"tam nakit", []domain.Payment{cash(12537)}, nil},
		{"fazla nakit", []domain.Payment{cash(13000)}, nil},
		{"fazla kart", []domain.Payment{card(13000)}, domain.ErrExcessNonCash},
		{"kart + nakit, fazlası nakitten az", []domain.Payment{card(10000), cash(5000)}, nil},
		{"kart + nakit, fazlası nakitten çok", []domain.Payment{card(13000), cash(100)}, domain.ErrExcessNonCash},
		{"eksik", []domain.Payment{cash(12000)}, domain.ErrInsufficientPayment},
		{"ödemesiz", nil, domain.ErrInsufficientPayment},
		{"sıfır tutar", []domain.Payment{cash(0), card(12537)}, domain.ErrInvalidPayment},
		{"negatif tutar", []domain.Payment{cash(-5)}, domain.ErrInvalidPayment},
		{"bilinmeyen yöntem", []domain.Payment{{Method: "cheque", Amount: 12537}}, domain.ErrInvalidPayment},
		{"tam yemek kartı", []domain.Payment{meal(12537)}, nil},
		{"yemek kartı + nakit", []domain.Payment{meal(10000), cash(3000)}, nil},
		{"fazla yemek kartı (para üstü yok)", []domain.Payment{meal(13000)}, domain.ErrExcessNonCash},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := newSale(t).CheckPayments(tt.payments); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRecordCharge(t *testing.T) {
	s := newSale(t)
	if err := s.RecordCharge([]domain.Payment{cash(1000)}); !errors.Is(err, domain.ErrInsufficientPayment) {
		t.Errorf("insufficient payment: err = %v, want %v", err, domain.ErrInsufficientPayment)
	}
	if s.Status() != domain.StatusOpen {
		t.Fatalf("a failed RecordCharge changed the status: %s", s.Status())
	}

	payments := []domain.Payment{{Method: domain.PaymentCard, Amount: 10000, Ref: "AUTH-1"}, cash(3000)}
	if err := s.RecordCharge(payments); err != nil {
		t.Fatalf("RecordCharge: %v", err)
	}
	if s.Status() != domain.StatusCharged || !reflect.DeepEqual(s.Payments(), payments) || s.Change() != 463 {
		t.Errorf("sale is wrong: %+v (change %d)", s.Snapshot(), s.Change())
	}
	if len(s.PullEvents()) != 0 {
		t.Error("no event should be published when the payment is taken; the event comes after the fiscal record")
	}
	if err := s.RecordCharge(payments); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("second RecordCharge: err = %v, want %v", err, domain.ErrNotOpen)
	}
	if err := s.Void(now); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("Void on a sale whose payment was taken: err = %v, want %v", err, domain.ErrNotOpen)
	}
}

func TestMarkPaid(t *testing.T) {
	s := newSale(t)
	if err := s.MarkPaid(1, "", now); !errors.Is(err, domain.ErrNotCharged) {
		t.Errorf("MarkPaid on a sale whose payment was not taken: err = %v, want %v", err, domain.ErrNotCharged)
	}
	if err := s.RecordCharge([]domain.Payment{cash(13000)}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPaid(0, "", now); !errors.Is(err, domain.ErrInvalidReceiptNo) {
		t.Errorf("receipt no 0: err = %v, want %v", err, domain.ErrInvalidReceiptNo)
	}
	if s.Status() != domain.StatusCharged {
		t.Errorf("a failed MarkPaid changed the status: %s", s.Status())
	}

	paidAt := now.Add(time.Minute)
	if err := s.MarkPaid(7, "SIM-000007", paidAt); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if s.Status() != domain.StatusPaid || s.ReceiptNo() != 7 || s.FiscalNo() != "SIM-000007" || !s.PaidAt().Equal(paidAt) {
		t.Errorf("sale status is wrong: %+v", s.Snapshot())
	}
	if got := s.Change(); got != 463 {
		t.Errorf("Change = %d, want 463", got)
	}

	events := s.PullEvents()
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	ev, ok := events[0].(domain.SaleCompleted)
	if !ok {
		t.Fatalf("event type %T, want SaleCompleted", events[0])
	}
	if ev.Name() != "sale.completed.v1" || ev.ReceiptNo != 7 || ev.Total != 12537 || ev.TaxTotal != 328 || ev.Change != 463 || !ev.OccurredAt().Equal(paidAt) {
		t.Errorf("event is wrong: %+v", ev)
	}
	if len(s.PullEvents()) != 0 {
		t.Error("PullEvents did not empty the list")
	}
	if err := s.MarkPaid(8, "", now); !errors.Is(err, domain.ErrNotCharged) {
		t.Errorf("second MarkPaid: err = %v, want %v", err, domain.ErrNotCharged)
	}
}

func TestVoid(t *testing.T) {
	s := newSale(t)
	if err := s.Void(now); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if s.Status() != domain.StatusVoided || !s.VoidedAt().Equal(now) {
		t.Errorf("sale status is wrong: %+v", s.Snapshot())
	}
	events := s.PullEvents()
	if len(events) != 1 {
		t.Fatalf("%d events, want 1", len(events))
	}
	if ev, ok := events[0].(domain.SaleVoided); !ok || ev.Total != 12537 || ev.Name() != "sale.voided.v1" {
		t.Errorf("event is wrong: %+v", events[0])
	}
	if err := s.Void(now); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("second Void: err = %v, want %v", err, domain.ErrNotOpen)
	}
	if err := s.RecordCharge([]domain.Payment{cash(12537)}); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("RecordCharge on a cancelled sale: err = %v, want %v", err, domain.ErrNotOpen)
	}
}

func TestSnapshotRestore(t *testing.T) {
	s := newSale(t)
	if err := s.RecordCharge([]domain.Payment{{Method: domain.PaymentCard, Amount: 12537, Ref: "AUTH-9"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPaid(3, "SIM-000003", now); err != nil {
		t.Fatal(err)
	}
	r := domain.Restore(s.Snapshot())
	if !reflect.DeepEqual(r.Snapshot(), s.Snapshot()) {
		t.Errorf("Restore differs: %+v, want %+v", r.Snapshot(), s.Snapshot())
	}
	if len(r.PullEvents()) != 0 {
		t.Error("Restore carried events")
	}

	lines := s.Lines()
	lines[0].Name = "değişti"
	if s.Lines()[0].Name == "değişti" {
		t.Error("the slice returned by Lines changed the sale's internal state")
	}
}

func TestRecordFiscalFailure(t *testing.T) {
	s := newSale(t)
	if err := s.RecordFiscalFailure("kağıt yok", now); !errors.Is(err, domain.ErrNotCharged) {
		t.Errorf("on a sale whose payment was not taken: err = %v, want %v", err, domain.ErrNotCharged)
	}
	payments := []domain.Payment{{Method: domain.PaymentCard, Amount: 12537, Ref: "AUTH-1"}}
	if err := s.RecordCharge(payments); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFiscalFailure("yazıcı yanıt vermiyor", now); err != nil {
		t.Fatal(err)
	}
	events := s.PullEvents()
	ev, ok := events[0].(domain.SaleCharged)
	if len(events) != 1 || !ok || ev.Name() != "sale.charged.v1" || ev.Total != 12537 || ev.Reason != "yazıcı yanıt vermiyor" ||
		!reflect.DeepEqual(ev.Payments, payments) || len(ev.Lines) != 2 || !ev.OccurredAt().Equal(now) {
		t.Fatalf("event: %+v", events)
	}
	// The status does not change: the sale waits to be completed at the register.
	if s.Status() != domain.StatusCharged {
		t.Errorf("status %s", s.Status())
	}
}

// A quantity or price above the limits is rejected instead of overflowing the totals.
func TestLineLimits(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	line := domain.Line{ProductID: "p", Name: "Ürün", Qty: calc.MaxQty + 1, UnitPrice: 100, TaxRate: 2000}
	if _, err := domain.NewSale("s1", "register-1", "ayse", []domain.Line{line}, now); !errors.Is(err, domain.ErrInvalidLine) || !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("quantity above the limit: %v", err)
	}
	line.Qty, line.UnitPrice = money.QtyScale, calc.MaxUnitPrice+1
	if _, err := domain.NewSale("s1", "register-1", "ayse", []domain.Line{line}, now); !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("price above the limit: %v", err)
	}
	line.UnitPrice = 100
	many := make([]domain.Line, calc.MaxLines+1)
	for i := range many {
		many[i] = line
	}
	if _, err := domain.NewSale("s1", "register-1", "ayse", many, now); !errors.Is(err, calc.ErrTooLarge) {
		t.Errorf("too many lines: %v", err)
	}
}
