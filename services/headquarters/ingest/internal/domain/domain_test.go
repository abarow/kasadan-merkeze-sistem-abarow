// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		src     *Source
		link    Link
		pending uint64
	}{
		{"stream kurulmadı", nil, LinkNever, 0},
		{"hiç haber yok", &Source{}, LinkNever, 0},
		{"bağlı", &Source{Seen: true, Since: time.Second, Lag: 4}, LinkConnected, 4},
		{"haber kesildi", &Source{Seen: true, Since: StaleAfter + time.Second, Lag: 4}, LinkDisconnected, 0},
		{"kaynak hatası", &Source{Seen: true, Since: time.Second, Error: "stream not found"}, LinkDisconnected, 0},
	} {
		st := Status("store-1", "Kadıköy", tt.src, now)
		if st.Link != tt.link || st.Pending != tt.pending || st.Name != "Kadıköy" {
			t.Errorf("%s: %+v", tt.name, st)
		}
	}
}

func TestValidate(t *testing.T) {
	now := time.Now()
	valid := []interface{ Validate() error }{
		Event{ID: "e", Subject: "sale.completed.v1", StoreID: "m", OccurredAt: now},
		Sale{ID: "s", StoreID: "m", RegisterID: "k", ReceiptNo: 1, PaidAt: now},
		Void{SaleID: "s", StoreID: "m", RegisterID: "k", At: now},
		Unfinished{SaleID: "s", StoreID: "m", RegisterID: "k", At: now},
		Shift{StoreID: "m", RegisterID: "k", ID: 1, OpenedAt: now},
	}
	for _, v := range valid {
		if err := v.Validate(); err != nil {
			t.Errorf("%T should be valid: %v", v, err)
		}
	}
	// A record with no store id is not stored without knowing whom it belongs to at headquarters.
	invalid := []error{
		Event{ID: "e", Subject: "s", OccurredAt: now}.Validate(),
		InStore("", Sale{ID: "s", RegisterID: "k", ReceiptNo: 1, PaidAt: now}.Validate()),
		Sale{ID: "s", StoreID: "m", RegisterID: "k", PaidAt: now}.Validate(),
		InStore("", Void{SaleID: "s", RegisterID: "k", At: now}.Validate()),
		Unfinished{SaleID: "s", StoreID: "m", RegisterID: "k"}.Validate(),
		Shift{StoreID: "m", RegisterID: "k", OpenedAt: now}.Validate(),
	}
	for i, err := range invalid {
		if !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("record %d should be incomplete: %v", i, err)
		}
	}
	if err := InStore("m", Sale{ID: "s", StoreID: "m", RegisterID: "k", ReceiptNo: 1, PaidAt: now}.Validate()); err != nil {
		t.Errorf("sale with a store: %v", err)
	}
}
