// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"
)

const pb = "application/protobuf"

// decodeAs decodes the event and converts it to the expected type.
func decodeAs[T any](subject, contentType string, data []byte) (T, error) {
	var zero T
	v, err := Decode(subject, contentType, data)
	if err != nil {
		return zero, err
	}
	return v.(T), nil
}

func TestDecodeSale(t *testing.T) {
	paid := time.Date(2026, 10, 26, 11, 5, 0, 0, time.UTC)
	data, err := proto.Marshal(&salesv1.SaleCompleted{
		EventId: "e1", StoreId: "store-1", RegisterId: "register-2", OccurredAt: timestamppb.New(paid.Add(time.Second)),
		Sale: &salesv1.Sale{
			Id: "s1", RegisterId: "register-2", Status: salesv1.SaleStatus_SALE_STATUS_PAID, CashierId: "ayse",
			ReceiptNo: 42, FiscalNo: "SIM-000042", TotalKurus: 13638, TaxTotalKurus: 135, ChangeKurus: 362,
			PaidAt: timestamppb.New(paid),
			Lines: []*salesv1.SaleLine{
				{ProductId: "p2", Barcode: "2700001012500", Name: "Domates", QtyMilli: 1250, UnitPriceKurus: 8990, TaxRateBp: 100},
			},
			Payments: []*salesv1.Payment{
				{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: 14000},
				{Method: salesv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED, AmountKurus: 1},
			},
			Taxes: []*salesv1.TaxLine{{RateBp: 100, GrossKurus: 13638, TaxKurus: 135}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := decodeAs[Sale]("sale.completed.v1", pb, data)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "s1" || s.StoreID != "store-1" || s.RegisterID != "register-2" || s.CashierID != "ayse" || s.ReceiptNo != 42 ||
		s.FiscalNo != "SIM-000042" || s.Total != 13638 || s.TaxTotal != 135 || s.Change != 362 || !s.PaidAt.Equal(paid) {
		t.Errorf("sale %+v", s)
	}
	if len(s.Lines) != 1 || s.Lines[0] != (Line{ProductID: "p2", Barcode: "2700001012500", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100}) ||
		s.Lines[0].Total() != 11238 {
		t.Errorf("lines %+v", s.Lines)
	}
	if len(s.Payments) != 2 || s.Payments[0] != (Payment{Method: Cash, Amount: 14000}) || s.Payments[1].Method != "unspecified" {
		t.Errorf("payments %+v", s.Payments)
	}
	if len(s.Taxes) != 1 || s.Taxes[0] != (Tax{Rate: 100, Gross: 13638, Tax: 135}) {
		t.Errorf("KDV %+v", s.Taxes)
	}
	if s.Validate() != nil {
		t.Error("the decoded sale is invalid")
	}
}

func TestDecodeVoid(t *testing.T) {
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	data, _ := proto.Marshal(&salesv1.SaleVoided{EventId: "e2", StoreId: "store-1", RegisterId: "register-1", OccurredAt: timestamppb.New(at),
		SaleId: "s9", TotalKurus: 999, CashierId: "mert", Refunded: true})
	v, err := decodeAs[Void]("sale.voided.v1", pb+"; charset=binary", data)
	if err != nil {
		t.Fatal(err)
	}
	if v.SaleID != "s9" || v.StoreID != "store-1" || v.RegisterID != "register-1" || v.CashierID != "mert" || v.Total != 999 || !v.Refunded || !v.At.Equal(at) {
		t.Errorf("void %+v", v)
	}
}

func TestDecodeUnfinished(t *testing.T) {
	at := time.Date(2026, 10, 26, 12, 30, 0, 0, time.UTC)
	data, _ := proto.Marshal(&salesv1.SaleCharged{EventId: "e4", StoreId: "store-1", RegisterId: "register-2", OccurredAt: timestamppb.New(at),
		Reason: "yazıcı yanıt vermiyor",
		Sale: &salesv1.Sale{Id: "s7", RegisterId: "register-2", CashierId: "mert", Status: salesv1.SaleStatus_SALE_STATUS_CHARGED, TotalKurus: 12537,
			Payments: []*salesv1.Payment{{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CARD, AmountKurus: 12537}}}})
	u, err := decodeAs[Unfinished]("sale.charged.v1", pb, data)
	if err != nil {
		t.Fatal(err)
	}
	if u.SaleID != "s7" || u.StoreID != "store-1" || u.RegisterID != "register-2" || u.CashierID != "mert" || u.Total != 12537 ||
		u.Reason != "yazıcı yanıt vermiyor" || !u.At.Equal(at) || u.Validate() != nil {
		t.Errorf("sale whose receipt could not be issued: %+v", u)
	}
	if _, err := decodeAs[Unfinished]("sale.charged.v1", "application/json", data); !errors.Is(err, ErrNotProtobuf) {
		t.Errorf("JSON: %v", err)
	}
}

func TestDecodeShift(t *testing.T) {
	opened := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	closedAt := opened.Add(8 * time.Hour)
	shift := &staffv1.Shift{Id: 4, CashierId: "ayse", CashierName: "Kasiyer Ayşe", OpenedAt: timestamppb.New(opened), OpeningCashKurus: 50000}
	data, _ := proto.Marshal(&staffv1.ShiftOpened{EventId: "e5", RegisterId: "register-1", OccurredAt: timestamppb.New(opened), Shift: shift})
	sh, err := decodeAs[Shift]("shift.opened.v1", pb, data)
	if err != nil || sh.RegisterID != "register-1" || sh.ID != 4 || sh.CashierName != "Kasiyer Ayşe" || !sh.OpenedAt.Equal(opened) || sh.OpeningCash != 50000 || !sh.Open() || sh.Validate() != nil {
		t.Errorf("opening: %+v, %v", sh, err)
	}

	closedShift := proto.Clone(shift).(*staffv1.Shift)
	closedShift.ClosedAt, closedShift.ClosedBy, closedShift.CountedCashKurus = timestamppb.New(closedAt), "ali", 59900
	data, _ = proto.Marshal(&staffv1.ShiftClosed{EventId: "e6", RegisterId: "register-1", OccurredAt: timestamppb.New(closedAt),
		Report: &staffv1.ShiftReport{Shift: closedShift, ReceiptCount: 12, TotalKurus: 12345, ExpectedCashKurus: 60000, DifferenceKurus: -100}})
	sh, err = decodeAs[Shift]("shift.closed.v1", pb, data)
	if err != nil || sh.ID != 4 || sh.Open() || !sh.ClosedAt.Equal(closedAt) || sh.ClosedBy != "ali" || sh.Receipts != 12 || sh.Total != 12345 ||
		sh.ExpectedCash != 60000 || sh.CountedCash != 59900 || sh.Difference != -100 {
		t.Errorf("closing: %+v, %v", sh, err)
	}

	// A closing event with no closing time is rejected.
	data, _ = proto.Marshal(&staffv1.ShiftClosed{RegisterId: "register-1", Report: &staffv1.ShiftReport{Shift: shift}})
	if _, err := decodeAs[Shift]("shift.closed.v1", pb, data); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("no closing time: %v", err)
	}
	if _, err := decodeAs[Shift]("shift.opened.v1", "application/json", nil); !errors.Is(err, ErrNotProtobuf) {
		t.Errorf("JSON: %v", err)
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := decodeAs[Sale]("sale.completed.v1", "application/json", []byte(`{"sale_id":"s1"}`)); !errors.Is(err, ErrNotProtobuf) {
		t.Errorf("JSON event: %v", err)
	}
	if _, err := decodeAs[Void]("sale.voided.v1", "", nil); !errors.Is(err, ErrNotProtobuf) {
		t.Errorf("no content type: %v", err)
	}
	if _, err := decodeAs[Sale]("sale.completed.v1", pb, []byte{0xff, 0xff}); err == nil {
		t.Error("a corrupt protobuf was decoded")
	}
	// An event with no sale is decoded but cannot be taken into the report (no receipt number).
	data, _ := proto.Marshal(&salesv1.SaleCompleted{EventId: "e3", RegisterId: "register-1"})
	s, err := decodeAs[Sale]("sale.completed.v1", pb, data)
	if err != nil || !errors.Is(s.Validate(), ErrInvalidEvent) {
		t.Errorf("event with no sale: %v %v", err, s.Validate())
	}
	// An event the ledger does not know is not decoded.
	if v, err := Decode("node.heartbeat.v1", "application/json", []byte("{}")); v != nil || err != nil {
		t.Errorf("unknown event: %v %v", v, err)
	}
}
