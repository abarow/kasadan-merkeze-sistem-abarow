// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package stock

import (
	"errors"
	"testing"
	"time"

	"abarow.com/pkg/money"
)

var (
	at       = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	products = map[string]Product{
		"ekmek":   {ID: "ekmek", Name: "Ekmek 250 g", Unit: Piece},
		"domates": {ID: "domates", Name: "Domates", Unit: Kg},
	}
)

func TestNewReceipt(t *testing.T) {
	m, err := NewReceipt("r1", " Un Değirmeni ", " IRS-42 ", "ali", at, []Line{{ProductID: "ekmek", Qty: 20000}, {ProductID: "domates", Qty: 12500}}, products)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != Receipt || m.Supplier != "Un Değirmeni" || m.Ref != "IRS-42" || len(m.Lines) != 2 || m.Lines[0].Name != "Ekmek 250 g" || m.Lines[1].Qty != 12500 {
		t.Errorf("%+v", m)
	}
	for name, c := range map[string]struct {
		supplier, ref string
		lines         []Line
		want          error
	}{
		"tedarikçisiz":      {"", "1", []Line{{ProductID: "ekmek", Qty: 1000}}, ErrNoSupplier},
		"irsaliyesiz":       {"T", " ", []Line{{ProductID: "ekmek", Qty: 1000}}, ErrNoRef},
		"satırsız":          {"T", "1", nil, ErrNoLines},
		"bilinmeyen ürün":   {"T", "1", []Line{{ProductID: "yok", Qty: 1000}}, ErrUnknownProduct},
		"sıfır":             {"T", "1", []Line{{ProductID: "ekmek", Qty: 0}}, ErrInvalidQty},
		"yarım adet":        {"T", "1", []Line{{ProductID: "ekmek", Qty: 500}}, ErrPieceFraction},
		"aynı ürün iki kez": {"T", "1", []Line{{ProductID: "ekmek", Qty: 1000}, {ProductID: "ekmek", Qty: 1000}}, ErrDuplicateProduct},
	} {
		if _, err := NewReceipt("r", c.supplier, c.ref, "ali", at, c.lines, products); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

// In a count the difference is counted − in the system; negative stock is corrected with a count,
// a line with a zero difference is recorded too.
func TestNewCount(t *testing.T) {
	system := map[string]money.Qty{"ekmek": -3000, "domates": 2000}
	m, err := NewCount("c1", "SAYIM-1", "ali", at, []Line{{ProductID: "ekmek", Counted: 5000}, {ProductID: "domates", Counted: 2000}}, system, products)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{
		{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 8000, Counted: 5000, System: -3000},
		{ProductID: "domates", Name: "Domates", Qty: 0, Counted: 2000, System: 2000},
	}
	for i, l := range want {
		if m.Lines[i] != l {
			t.Errorf("line %d: %+v, want %+v", i, m.Lines[i], l)
		}
	}
	if _, err := NewCount("c2", "", "ali", at, []Line{{ProductID: "ekmek", Counted: -1000}}, system, products); !errors.Is(err, ErrNegativeCount) {
		t.Errorf("negative count: %v", err)
	}
}

// A sale movement is unique by the sale id; the lines of the same product
// are combined, the quantities are negative.
func TestNewSale(t *testing.T) {
	m := NewSale("s1", "ayse", at, []Line{{ProductID: "ekmek", Name: "Ekmek", Qty: 2000}, {ProductID: "domates", Name: "Domates", Qty: 1250}, {ProductID: "ekmek", Name: "Ekmek", Qty: 1000}})
	if m.ID != "sale:s1" || m.Kind != Sale || len(m.Lines) != 2 || m.Lines[0].Qty != -3000 || m.Lines[1].Qty != -1250 {
		t.Errorf("%+v", m)
	}
}

// Transfer: out is negative, in is positive; one that came short is entered as less; only the receiving
// place, only once, takes delivery.
func TestTransfer(t *testing.T) {
	lines := []Line{{ProductID: "ekmek", Qty: 10000}, {ProductID: "domates", Qty: 2500}}
	out, tr, err := NewTransferOut("m1", "T-1", "headquarters", "store-1", "Merkez", at, lines, products)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != TransferOut || out.Ref != "T-1" || out.Counterpart != "store-1" || out.Lines[0].Qty != -10000 || tr.Lines[1].Qty != 2500 || !tr.Pending() {
		t.Errorf("outflow: %+v %+v", out, tr)
	}
	if _, _, err := NewTransferOut("m", "T", "headquarters", " ", "x", at, lines, products); !errors.Is(err, ErrNoDestination) {
		t.Errorf("hedefsiz: %v", err)
	}
	if _, _, err := NewTransferOut("m", "T", "headquarters", "headquarters", "x", at, lines, products); !errors.Is(err, ErrSameLocation) {
		t.Errorf("kendine: %v", err)
	}
	in, err := NewTransferIn("m2", "store-1", "Müdür", at, tr, []Line{{ProductID: "ekmek", Qty: 9000}, {ProductID: "domates", Qty: 2500}}, products)
	if err != nil || in.Kind != TransferIn || in.Counterpart != "headquarters" || in.Lines[0].Qty != 9000 {
		t.Fatalf("inflow: %+v %v", in, err)
	}
	if all, err := NewTransferIn("m3", "store-1", "Müdür", at, tr, nil, products); err != nil || all.Lines[0].Qty != 10000 {
		t.Errorf("all received: %+v %v", all, err)
	}
	// A product not written has not come: its line is recorded with zero.
	if part, err := NewTransferIn("m3", "store-1", "Müdür", at, tr, []Line{{ProductID: "domates", Qty: 2000}}, products); err != nil ||
		len(part.Lines) != 2 || part.Lines[0].ProductID != "ekmek" || part.Lines[0].Qty != 0 || part.Lines[1].Qty != 2000 {
		t.Errorf("came short: %+v %v", part, err)
	}
	if _, err := NewTransferIn("m3", "store-1", "Müdür", at, tr, []Line{{ProductID: "su", Qty: 1000}}, products); !errors.Is(err, ErrUnknownProduct) {
		t.Errorf("product not sent: %v", err)
	}
	if _, err := NewTransferIn("m4", "store-2", "x", at, tr, nil, products); !errors.Is(err, ErrNotForHere) {
		t.Errorf("another place: %v", err)
	}
	tr.ReceivedAt = at
	if _, err := NewTransferIn("m5", "store-1", "x", at, tr, nil, products); !errors.Is(err, ErrTransferReceived) {
		t.Errorf("second time: %v", err)
	}
}
