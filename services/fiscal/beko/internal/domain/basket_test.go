// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"encoding/json"
	"errors"
	"testing"

	"abarow.com/pkg/money"
)

var cfg = Config{Sections: map[money.Rate]int{1000: 1, 2000: 2}}

func receipt() Receipt {
	return Receipt{
		SaleID: "s1",
		Lines: []Line{
			{Name: "ŞAMPUAN", Qty: 2000, UnitPrice: 1500, Total: 3000, TaxRate: 2000},
			{Name: "Ekmek", Qty: 1000, UnitPrice: 1200, Total: 1200, TaxRate: 1000},
		},
		Total:    4200,
		Payments: []Payment{{Method: "cash", Amount: 5000}},
		Change:   800,
	}
}

func TestNewBasket(t *testing.T) {
	b, err := NewBasket(receipt(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := b.JSON()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["basketID"] != "s1" || got["documentType"] != 0.0 || got["isVoid"] != false || got["customerInfo"] != nil {
		t.Errorf("sepet: %s", data)
	}
	items := got["items"].([]any)
	first := items[0].(map[string]any)
	if first["name"] != "ŞAMPUAN" || first["price"] != 1500.0 || first["quantity"] != 2000.0 || first["sectionNo"] != 2.0 || first["taxPercent"] != 2000.0 {
		t.Errorf("kalem: %v", first)
	}
	if items[1].(map[string]any)["sectionNo"] != 1.0 {
		t.Errorf("section of the second item: %v", items[1])
	}
	pay := got["paymentItems"].([]any)[0].(map[string]any)
	if pay["amount"] != 5000.0 || pay["type"] != 1.0 {
		t.Errorf("payment (amount taken): %v", pay)
	}
	// Net cash: the sum of the payments equals the receipt total.
	net := cfg
	net.CashNet = true
	b, err = NewBasket(receipt(), net)
	if err != nil || b.PaymentItems[0].Amount != 4200 {
		t.Errorf("net nakit: %+v %v", b.PaymentItems, err)
	}
}

func TestPaymentTypes(t *testing.T) {
	r := receipt()
	r.Payments = []Payment{{Method: "card", Amount: 3000, Collect: true}, {Method: "meal_card", Amount: 1200, Collect: true}}
	r.Change = 0
	b, err := NewBasket(r, cfg)
	if err != nil || b.PaymentItems[0].Type != 3 || b.PaymentItems[1].Type != 7 {
		t.Fatalf("card payments: %+v %v", b.PaymentItems, err)
	}
	// A card that is not marked as one the device will collect is not written on the receipt (double charge).
	r.Payments[0].Collect = false
	if _, err := NewBasket(r, cfg); !errors.Is(err, ErrCardNotCollected) {
		t.Errorf("collect'siz kart: %v", err)
	}
	r.Payments[0].Collect = true
	// Nakit Collect istemez.
	if _, err := NewBasket(receipt(), cfg); err != nil {
		t.Errorf("nakit: %v", err)
	}
	r.Payments = []Payment{{Method: "other", Amount: 4200}}
	if _, err := NewBasket(r, cfg); !errors.Is(err, ErrInvalidReceipt) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestInvalid(t *testing.T) {
	r := receipt()
	r.Lines[0].TaxRate = 800
	if _, err := NewBasket(r, cfg); !errors.Is(err, ErrUnmappedTax) {
		t.Errorf("VAT with no section: %v", err)
	}
	r = receipt()
	r.Total = 4300
	if _, err := NewBasket(r, cfg); !errors.Is(err, ErrInvalidReceipt) {
		t.Errorf("toplam tutmuyor: %v", err)
	}
	r = receipt()
	r.Change = 100
	if _, err := NewBasket(r, cfg); !errors.Is(err, ErrInvalidReceipt) {
		t.Errorf("payment does not match: %v", err)
	}
	if _, err := NewBasket(Receipt{}, cfg); !errors.Is(err, ErrInvalidReceipt) {
		t.Errorf("empty receipt: %v", err)
	}
}

func TestSaleStatus(t *testing.T) {
	s, err := ParseSaleStatus([]byte(`{"basketID":"s1","documentType":0,"message":"OK","paymentCount":1,"receiptNo":2,"status":0,"zNo":12}`))
	if err != nil || s.Status != StatusOK || s.FiscalNo() != "Z0012-000002" {
		t.Errorf("durum: %+v %v", s, err)
	}
	if _, err := ParseSaleStatus([]byte(`{}`)); err == nil {
		t.Error("sepet kimliksiz durum kabul edildi")
	}
	if _, err := ParseSaleStatus([]byte(`yok`)); err == nil {
		t.Error("bozuk JSON kabul edildi")
	}
}
