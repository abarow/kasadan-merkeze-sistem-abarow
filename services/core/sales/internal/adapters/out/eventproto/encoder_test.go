// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package eventproto

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/services/core/sales/internal/domain"
)

func TestEncode(t *testing.T) {
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	b, ct, err := Encoder{}.Encode(domain.SaleCompleted{
		SaleID: "s1", RegisterID: "register-1", CashierID: "ayse", ReceiptNo: 12, FiscalNo: "SIM-000012",
		Total: 4900, TaxTotal: 462, Change: 100,
		Taxes:    []domain.TaxLine{{Rate: 2000, Gross: 1500, Tax: 250}},
		Lines:    []domain.Line{{ProductID: "p5", Name: "ŞAMPUAN", Qty: 1000, UnitPrice: 1500, TaxRate: 2000}},
		Payments: []domain.Payment{{Method: domain.PaymentCash, Amount: 3000}, {Method: domain.PaymentCard, Amount: 2000, Ref: "POS-1"}},
		At:       at,
	}, "e1", "store-1")
	if err != nil || ct != ContentType {
		t.Fatalf("Encode: %q, %v", ct, err)
	}
	var got salesv1.SaleCompleted
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	s := got.GetSale()
	if got.GetEventId() != "e1" || got.GetStoreId() != "store-1" || !got.GetOccurredAt().AsTime().Equal(at) ||
		s.GetId() != "s1" || s.GetCashierId() != "ayse" || s.GetStatus() != salesv1.SaleStatus_SALE_STATUS_PAID ||
		s.GetTotalKurus() != 4900 || s.GetChangeKurus() != 100 || s.GetReceiptNo() != 12 || len(s.GetTaxes()) != 1 ||
		s.GetLines()[0].GetName() != "ŞAMPUAN" || s.GetPayments()[0].GetMethod() != salesv1.PaymentMethod_PAYMENT_METHOD_CASH ||
		s.GetPayments()[1].GetRef() != "POS-1" {
		t.Errorf("çözülen olay yanlış: %v", &got)
	}

	b, _, err = Encoder{}.Encode(domain.SaleVoided{SaleID: "s2", CashierID: "ayse", Total: 1299, Refunded: true, At: at}, "e2", "store-1")
	if err != nil {
		t.Fatal(err)
	}
	var voided salesv1.SaleVoided
	if err := proto.Unmarshal(b, &voided); err != nil || voided.GetSaleId() != "s2" || !voided.GetRefunded() || voided.GetCashierId() != "ayse" {
		t.Errorf("iptal olayı: %v, %v", &voided, err)
	}

	b, _, err = Encoder{}.Encode(domain.SaleCharged{
		SaleID: "s3", RegisterID: "register-1", CashierID: "ayse", Total: 1500, Reason: "yazıcı yanıt vermiyor",
		Lines:    []domain.Line{{ProductID: "p5", Name: "ŞAMPUAN", Qty: 1000, UnitPrice: 1500, TaxRate: 2000}},
		Payments: []domain.Payment{{Method: domain.PaymentCard, Amount: 1500, Ref: "POS-2"}},
		At:       at,
	}, "e3", "store-1")
	if err != nil {
		t.Fatal(err)
	}
	var charged salesv1.SaleCharged
	if err := proto.Unmarshal(b, &charged); err != nil || charged.GetReason() != "yazıcı yanıt vermiyor" ||
		charged.GetSale().GetId() != "s3" || charged.GetSale().GetStatus() != salesv1.SaleStatus_SALE_STATUS_CHARGED ||
		charged.GetSale().GetTotalKurus() != 1500 || charged.GetSale().GetPayments()[0].GetRef() != "POS-2" ||
		len(charged.GetSale().GetLines()) != 1 || charged.GetRegisterId() != "register-1" {
		t.Errorf("fişi kesilemeyen satış olayı: %v, %v", &charged, err)
	}
}
