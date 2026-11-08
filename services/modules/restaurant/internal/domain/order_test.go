// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"time"

	"abarow.com/pkg/menu"
)

var (
	at       = time.Date(2026, 10, 30, 19, 0, 0, 0, time.UTC)
	meatball = Product{ID: "kofte", Name: "Köfte", UnitPrice: 32000, TaxRate: 1000, Options: []menu.Group{
		{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az pişmiş"}, {ID: "iyi", Name: "iyi pişmiş"}}},
		{ID: "ekstra", Name: "Ekstralar", Max: 2, Options: []menu.Option{{ID: "kasar", Name: "kaşar", PriceDelta: 2500}}},
	}}
	yogurtdrink = Product{ID: "ayran", Name: "Ayran", UnitPrice: 4000, TaxRate: 1000, Station: menu.Bar}
)

func add(t *testing.T, o *Order, id string, p Product, qty int, choices ...menu.Choice) {
	t.Helper()
	if _, err := o.AddLine(NewLineInput{ID: id, Product: p, Choices: choices, Qty: qty, By: "Ali", At: at}); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
}

// Line: the price with the option differences, the same id does not open a second line,
// it cannot be added without choosing a required option.
func TestAddLine(t *testing.T) {
	o := NewOrder("o1", "m1", "Ali", at)
	add(t, o, "l-1", meatball, 2, menu.Choice{Group: "pisirme", Options: []string{"az"}}, menu.Choice{Group: "ekstra", Options: []string{"kasar"}})
	l := o.Lines[0]
	if l.UnitPrice != 34500 || l.Label() != "Köfte (az pişmiş, kaşar)" || l.Station != menu.Kitchen || l.Status != Pending ||
		l.Options[1].Group != "ekstra" || o.Total() != 69000 || o.Version != 2 {
		t.Errorf("line %+v, total %v, version %d", l, o.Total(), o.Version)
	}
	if added, err := o.AddLine(NewLineInput{ID: "l-1", Product: yogurtdrink, Qty: 1}); err != nil || added || len(o.Lines) != 1 {
		t.Errorf("request that came again: %v %v", added, err)
	}
	if _, err := o.AddLine(NewLineInput{ID: "l-2", Product: meatball, Qty: 1}); !errors.Is(err, menu.ErrChoice) {
		t.Errorf("zorunlu opsiyon: %v", err)
	}
	if _, err := o.AddLine(NewLineInput{ID: "l 3", Product: yogurtdrink, Qty: 1}); !errors.Is(err, ErrInvalidLine) {
		t.Errorf("invalid id: %v", err)
	}
	if _, err := o.AddLine(NewLineInput{ID: "l-4", Product: yogurtdrink, Qty: 100}); !errors.Is(err, ErrInvalidLine) {
		t.Errorf("100 pieces: %v", err)
	}
	if _, err := o.AddLine(NewLineInput{ID: "l-5", Product: Product{ID: "balik", Name: "Levrek", Weighed: true}, Qty: 1}); !errors.Is(err, ErrInvalidLine) {
		t.Errorf("weighed product: %v", err)
	}
}

// Kitchen: an unsent line can change; what is sent is sent once,
// then it can only be cancelled with a reason (a partial cancellation becomes a separate line).
func TestSendAndVoid(t *testing.T) {
	o := NewOrder("o1", "m1", "Ali", at)
	add(t, o, "l-1", yogurtdrink, 3)
	add(t, o, "l-2", yogurtdrink, 1)
	if err := o.SetLine("l-2", 0, ""); err != nil || len(o.Lines) != 1 {
		t.Fatalf("deleting a pending line: %v", err)
	}
	if err := o.SetLine("l-1", 2, "  buzsuz  "); err != nil || o.Lines[0].Qty != 2 || o.Lines[0].Note != "buzsuz" {
		t.Fatalf("count and note: %+v %v", o.Lines[0], err)
	}
	if _, err := o.Void("l-1", "v-1", 1, "yanlış", "Müdür"); !errors.Is(err, ErrLineNotSent) {
		t.Errorf("cancelling one that was not sent: %v", err)
	}
	sent, err := o.Send(at, "")
	if err != nil || len(sent) != 1 || sent[0].Status != Sent {
		t.Fatalf("send: %+v %v", sent, err)
	}
	if again, _ := o.Send(at, ""); len(again) != 0 {
		t.Errorf("second send %+v", again)
	}
	if err := o.SetLine("l-1", 1, ""); !errors.Is(err, ErrLineSent) {
		t.Errorf("changing one that was sent: %v", err)
	}
	if _, err := o.Void("l-1", "v-1", 1, " ", "Müdür"); !errors.Is(err, ErrVoidReason) {
		t.Errorf("void without a reason: %v", err)
	}
	v, err := o.Void("l-1", "v-1", 1, "döküldü", "Müdür")
	if err != nil || v.ID != "v-1" || v.Qty != 1 || v.Status != Voided || o.Lines[0].Qty != 1 || o.Total() != 4000 {
		t.Fatalf("partial cancellation: %+v %v; lines %+v", v, err, o.Lines)
	}
	if v, err := o.Void("l-1", "", 1, "müşteri vazgeçti", "Müdür"); err != nil || v.ID != "l-1" || o.Total() != 0 {
		t.Errorf("cancelling all: %+v %v", v, err)
	}
}

// Payment and bill splitting: the selected quantities wait in payment; once paid the rest
// stays open, when everything is paid the check closes; a cancelled payment returns to
// the check; while a payment is in progress there is no second payment or move.
func TestPayAndSplit(t *testing.T) {
	o := NewOrder("o1", "m1", "Ali", at)
	add(t, o, "l-1", yogurtdrink, 3)
	add(t, o, "l-2", meatball, 1, menu.Choice{Group: "pisirme", Options: []string{"iyi"}})
	if _, err := o.Parts(nil); !errors.Is(err, ErrNotSent) {
		t.Errorf("payment before sending: %v", err)
	}
	o.Send(at, "")
	parts, err := o.Parts([]Part{{LineID: "l-1", Qty: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Parts([]Part{{LineID: "l-1", Qty: 4}}); !errors.Is(err, ErrQtyTooLarge) {
		t.Errorf("too many: %v", err)
	}
	if err := o.Hold("s1", parts, "Ali", at); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Parts(nil); !errors.Is(err, ErrPaymentPending) {
		t.Errorf("second payment with a payment in progress: %v", err)
	}
	if err := o.MoveTo("m2"); !errors.Is(err, ErrPaymentPending) {
		t.Errorf("moving with a payment in progress: %v", err)
	}
	// Sale cancelled: the quantities return to the check.
	if o.Settle(false, at) || o.Lines[0].Held != 0 || o.Lines[0].OpenQty() != 3 {
		t.Fatalf("cancelled payment: %+v", o.Lines[0])
	}
	o.Hold("s2", parts, "Ali", at)
	if closed := o.Settle(true, at); closed || o.Lines[0].Paid != 2 || o.Due() != 4000+32000 {
		t.Fatalf("partial payment: closed=%v %+v remaining %v", closed, o.Lines[0], o.Due())
	}
	rest, err := o.Parts(nil)
	if err != nil || len(rest) != 2 || rest[0].Qty != 1 {
		t.Fatalf("left: %+v %v", rest, err)
	}
	o.Hold("s3", rest, "Ali", at)
	if !o.Settle(true, at) || !o.Closed || o.Due() != 0 {
		t.Errorf("all paid but the check did not close: %+v", o)
	}
	if _, err := o.AddLine(NewLineInput{ID: "l-9", Product: yogurtdrink, Qty: 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("kapanan adisyona ekleme: %v", err)
	}
}

// Cancelled lines are not paid; when the rest is paid the check closes.
func TestVoidedLinesDoNotBlockClose(t *testing.T) {
	o := NewOrder("o1", "m1", "Ali", at)
	add(t, o, "l-1", yogurtdrink, 1)
	add(t, o, "l-2", yogurtdrink, 1)
	o.Send(at, "")
	o.Void("l-2", "", 1, "döküldü", "Müdür")
	parts, err := o.Parts(nil)
	if err != nil || len(parts) != 1 {
		t.Fatalf("%+v %v", parts, err)
	}
	o.Hold("s1", parts, "Ali", at)
	if !o.Settle(true, at) {
		t.Error("the check did not close")
	}
}

// Moving and merging; the version check.
func TestMoveAndMerge(t *testing.T) {
	a := NewOrder("o1", "m1", "Ali", at)
	b := NewOrder("o2", "m2", "Ayşe", at)
	add(t, a, "l-1", yogurtdrink, 1)
	add(t, b, "l-2", yogurtdrink, 2)
	if err := a.Check(1); !errors.Is(err, ErrStale) {
		t.Errorf("old version: %v", err)
	}
	if err := a.MoveTo("m3"); err != nil || a.TableID != "m3" {
		t.Errorf("moving: %v", err)
	}
	if err := a.Absorb(b, at); err != nil || len(a.Lines) != 2 || !b.Closed || len(b.Lines) != 0 {
		t.Errorf("merging: %v %+v %+v", err, a, b)
	}
	if err := a.Absorb(b, at); !errors.Is(err, ErrClosed) {
		t.Errorf("merging with a closed check: %v", err)
	}
	e := NewOrder("o3", "m4", "Ali", at)
	if err := e.CloseEmpty(at); err != nil || !e.Closed {
		t.Errorf("closing an empty check: %v", err)
	}
	if err := a.CloseEmpty(at); err == nil {
		t.Error("a check with lines closed as empty")
	}
}

func TestValidateFloor(t *testing.T) {
	ok := []Area{{ID: "ic", Name: "İç salon", Tables: []Table{{ID: "1", Name: "1"}, {ID: "2", Name: "2"}}}, {ID: "bahce", Name: "Bahçe", Tables: []Table{{ID: "b1", Name: "B1"}}}}
	if err := ValidateFloor(ok); err != nil {
		t.Fatal(err)
	}
	bad := [][]Area{
		{{ID: "ic", Name: "İç", Tables: []Table{{ID: "1", Name: "1"}}}, {ID: "dis", Name: "Dış", Tables: []Table{{ID: "1", Name: "1"}}}},
		{{ID: "ic", Name: " "}},
		{{ID: "i c", Name: "İç"}},
	}
	for i, f := range bad {
		if err := ValidateFloor(f); !errors.Is(err, ErrFloor) {
			t.Errorf("%d: %v", i, err)
		}
	}
}
