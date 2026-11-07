// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/services/headquarters/ingest/internal/domain"
)

const pb = "application/protobuf"

func headers(id, source string, at time.Time) nats.Header {
	h := nats.Header{}
	h.Set("ce-id", id)
	h.Set("ce-source", source)
	h.Set("ce-time", at.Format(time.RFC3339Nano))
	h.Set("content-type", pb)
	return h
}

func TestParseCompleted(t *testing.T) {
	paid := time.Date(2026, 10, 27, 11, 5, 0, 0, time.UTC)
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
	in, err := Parse("store-1.sale.completed.v1", headers("e1", "abarow/store-1/register-2", paid.Add(time.Second)), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := in.Event
	if e.ID != "e1" || e.Subject != "sale.completed.v1" || e.StoreID != "store-1" || e.RegisterID != "register-2" ||
		!e.OccurredAt.Equal(paid.Add(time.Second)) || e.ContentType != pb || string(e.Payload) != string(data) || in.ClaimedStore != "" {
		t.Errorf("olay %+v", in)
	}
	s, ok := in.Body.(domain.Sale)
	if !ok {
		t.Fatalf("body %T", in.Body)
	}
	if s.ID != "s1" || s.StoreID != "store-1" || s.RegisterID != "register-2" || s.CashierID != "ayse" || s.ReceiptNo != 42 ||
		s.FiscalNo != "SIM-000042" || s.Total != 13638 || s.TaxTotal != 135 || s.Change != 362 || !s.PaidAt.Equal(paid) {
		t.Errorf("sale %+v", s)
	}
	if len(s.Lines) != 1 || s.Lines[0] != (domain.Line{ProductID: "p2", Barcode: "2700001012500", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100}) ||
		s.Lines[0].Total() != 11238 {
		t.Errorf("lines %+v", s.Lines)
	}
	if len(s.Payments) != 2 || s.Payments[0] != (domain.Payment{Method: domain.Cash, Amount: 14000}) || s.Payments[1].Method != "unspecified" {
		t.Errorf("payments %+v", s.Payments)
	}
	if len(s.Taxes) != 1 || s.Taxes[0] != (domain.Tax{Rate: 100, Gross: 13638, Tax: 135}) {
		t.Errorf("KDV %+v", s.Taxes)
	}
}

// The store the event came from is used, not the store written inside the event.
func TestParseStoreFromRoute(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	data, _ := proto.Marshal(&salesv1.SaleVoided{EventId: "e2", StoreId: "store-9", RegisterId: "register-1", OccurredAt: timestamppb.New(at),
		SaleId: "s9", TotalKurus: 999, CashierId: "mert", Refunded: true})
	in, err := Parse("store-1.sale.voided.v1", headers("e2", "abarow/store-9/register-1", at), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v := in.Body.(domain.Void)
	if in.Event.StoreID != "store-1" || v.StoreID != "store-1" || in.ClaimedStore != "store-9" ||
		v.SaleID != "s9" || v.RegisterID != "register-1" || v.CashierID != "mert" || v.Total != 999 || !v.Refunded || !v.At.Equal(at) {
		t.Errorf("iptal %+v", in)
	}
	// An old register with no store id configured: the event is still written to the store it came from.
	data, _ = proto.Marshal(&salesv1.SaleCharged{EventId: "e3", RegisterId: "register-1", OccurredAt: timestamppb.New(at), Reason: "yazıcı kapalı",
		Sale: &salesv1.Sale{Id: "s10", CashierId: "mert", TotalKurus: 500}})
	in, err = Parse("store-1.sale.charged.v1", headers("e3", "abarow//register-1", at), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u := in.Body.(domain.Unfinished)
	if in.ClaimedStore != "" || u.StoreID != "store-1" || u.RegisterID != "register-1" || u.SaleID != "s10" || u.Total != 500 ||
		u.Reason != "yazıcı kapalı" || !u.At.Equal(at) || u.Validate() != nil {
		t.Errorf("sale whose receipt could not be issued %+v", in)
	}
}

func TestParseShift(t *testing.T) {
	opened := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	closed := opened.Add(9 * time.Hour)
	shift := &staffv1.Shift{Id: 7, CashierId: "ayse", CashierName: "Ayşe", OpenedAt: timestamppb.New(opened), OpeningCashKurus: 50000}
	data, _ := proto.Marshal(&staffv1.ShiftOpened{EventId: "e4", StoreId: "store-1", RegisterId: "register-1", Shift: shift})
	in, err := Parse("store-1.shift.opened.v1", headers("e4", "abarow/store-1/register-1", opened), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sh := in.Body.(domain.Shift); sh.StoreID != "store-1" || sh.RegisterID != "register-1" || sh.ID != 7 || sh.CashierName != "Ayşe" ||
		sh.OpeningCash != 50000 || !sh.OpenedAt.Equal(opened) || !sh.ClosedAt.IsZero() {
		t.Errorf("opened shift %+v", sh)
	}

	closedShift := proto.Clone(shift).(*staffv1.Shift)
	closedShift.ClosedAt, closedShift.ClosedBy, closedShift.CountedCashKurus = timestamppb.New(closed), "mert", 149000
	data, _ = proto.Marshal(&staffv1.ShiftClosed{EventId: "e5", StoreId: "store-1", RegisterId: "register-1", Report: &staffv1.ShiftReport{
		Shift: closedShift, ReceiptCount: 31, TotalKurus: 120000, ExpectedCashKurus: 150000, DifferenceKurus: -1000}})
	in, err = Parse("store-1.shift.closed.v1", headers("e5", "abarow/store-1/register-1", closed), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sh := in.Body.(domain.Shift); sh.ID != 7 || !sh.ClosedAt.Equal(closed) || sh.ClosedBy != "mert" || sh.Receipts != 31 ||
		sh.Total != 120000 || sh.ExpectedCash != 150000 || sh.CountedCash != 149000 || sh.Difference != -1000 {
		t.Errorf("kapanan vardiya %+v", sh)
	}

	// A closing event with no closing time is incomplete.
	data, _ = proto.Marshal(&staffv1.ShiftClosed{EventId: "e6", RegisterId: "register-1", Report: &staffv1.ShiftReport{Shift: shift}})
	if _, err := Parse("store-1.shift.closed.v1", headers("e6", "", closed), data, time.Now()); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("no closing time: %v", err)
	}
}

func TestParseRaw(t *testing.T) {
	stored := time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)
	// An unknown event kind comes in its raw form; the id and time from the fallbacks.
	h := nats.Header{}
	h.Set(nats.MsgIdHdr, "e7")
	h.Set("ce-source", "abarow/store-1/register-3")
	in, err := Parse("store-1.node.heartbeat.v1", h, []byte("{}"), stored)
	if err != nil {
		t.Fatal(err)
	}
	if in.Body != nil || in.Event.ID != "e7" || in.Event.Subject != "node.heartbeat.v1" || in.Event.RegisterID != "register-3" ||
		!in.Event.OccurredAt.Equal(stored) || in.Event.Validate() != nil {
		t.Errorf("ham olay %+v", in)
	}

	// An old JSON sale event and a subject with no store are rejected.
	h.Set("content-type", "application/json")
	if _, err := Parse("store-1.sale.completed.v1", h, []byte("{}"), stored); !errors.Is(err, ErrNotProtobuf) {
		t.Errorf("JSON olay: %v", err)
	}
	if _, err := Parse("sale", h, nil, stored); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("subject with no store: %v", err)
	}
	if _, err := Parse("store-1.sale.completed.v1", headers("e8", "", stored), []byte{0xff}, stored); err == nil {
		t.Error("a corrupt protobuf was decoded")
	}
}

// The store price event comes from the store server itself (it has no register).
func TestParseStorePrices(t *testing.T) {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	data, _ := proto.Marshal(&catalogv1.StorePrices{EventId: "e9", OccurredAt: timestamppb.New(at), StoreId: "store-1", ProductId: "ekmek",
		ProductName: "Ekmek 250 g", UpdatedBy: "mağaza", Prices: []*catalogv1.StorePriceChange{
			{UnitPriceKurus: 1800}, {From: timestamppb.New(monday), Clear: true}}})
	in, err := Parse("store-1.store.price.changed.v1", headers("e9", "abarow/store-1/", at), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := in.Body.(domain.StorePrices)
	if !ok || in.Event.RegisterID != "" || sp.StoreID != "store-1" || sp.ProductID != "ekmek" || sp.ProductName != "Ekmek 250 g" ||
		sp.UpdatedBy != "mağaza" || !sp.At.Equal(at) || len(sp.Prices) != 2 || sp.Prices[0] != (domain.StorePrice{Amount: 1800}) ||
		!sp.Prices[1].Clear || !sp.Prices[1].From.Equal(monday) || sp.Validate() != nil {
		t.Errorf("store price %+v", in)
	}
}

// The cashier event comes from the store server itself; the store is determined from the source
// in headquarters' configuration.
func TestParseCashier(t *testing.T) {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	data, _ := proto.Marshal(&staffv1.CashierState{EventId: "e10", OccurredAt: timestamppb.New(at), StoreId: "store-9", CashierId: "zeynep",
		Name: "Zeynep Kaya", Role: "manager", Disabled: true, UpdatedBy: "mağaza"})
	in, err := Parse("store-1.store.cashier.changed.v1", headers("e10", "abarow/store-1/", at), data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, ok := in.Body.(domain.Cashier)
	if !ok || in.Event.RegisterID != "" || c.StoreID != "store-1" || in.ClaimedStore != "store-9" || c.CashierID != "zeynep" ||
		c.Name != "Zeynep Kaya" || c.Role != "manager" || !c.Disabled || c.UpdatedBy != "mağaza" || !c.At.Equal(at) || c.Validate() != nil {
		t.Errorf("kasiyer %+v", in)
	}
}
