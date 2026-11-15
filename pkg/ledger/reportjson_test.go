// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPeriod(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Skip("no time zone data")
	}
	start, end, err := Period("2026-10-01", "2026-10-31", loc)
	if err != nil || !start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, loc)) || !end.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("period: %v %v %v", start, end, err)
	}
	if got := Dates(start, end); len(got) != 31 || got[0] != "2026-10-01" || got[30] != "2026-10-31" {
		t.Errorf("days: %v", got)
	}
	// A single day; it can last 23 hours at a daylight saving change (2016).
	d0, d1, err := Day("2026-10-26", loc)
	if err != nil || d1.Sub(d0) != 24*time.Hour {
		t.Errorf("day: %v %v %v", d0, d1, err)
	}
	for name, tc := range map[string][2]string{
		"başlangıç": {"26.10.2026", "2026-10-26"},
		"bitiş":     {"2026-10-26", "yarın"},
		"ters":      {"2026-10-26", "2026-10-25"},
		"uzun":      {"2025-01-01", "2026-10-26"},
	} {
		if _, _, err := Period(tc[0], tc[1], loc); err == nil || !errors.Is(err, ErrInvalidPeriod) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReportJSON(t *testing.T) {
	r := Report{
		Payments: []PaymentTotal{{Method: "cash", Amount: 1200}},
		Taxes:    []Tax{{Rate: 1000, Gross: 1100, Tax: 100}},
		Hours:    []HourTotal{{Hour: 9, Receipts: 2, Total: 1200}},
		Days:     []DayTotal{{Date: "2026-10-26", Receipts: 2, Total: 1200}},
		Products: []ProductTotal{{ProductID: "p1", Name: "Çay", Qty: 1500, Total: 1200}},
	}
	b, err := json.Marshal(map[string]any{
		"payments": r.PaymentsJSON(), "taxes": r.TaxesJSON(), "hours": r.HoursJSON(), "days": r.DaysJSON(), "products": r.ProductsJSON(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"days":[{"date":"2026-10-26","receipt_count":2,"total_kurus":1200}],"hours":[{"hour":9,"receipt_count":2,"total_kurus":1200}],` +
		`"payments":[{"method":"cash","amount_kurus":1200}],"products":[{"product_id":"p1","name":"Çay","qty_milli":1500,"total_kurus":1200}],` +
		`"taxes":[{"rate_bp":1000,"gross_kurus":1100,"tax_kurus":100}]}`
	if string(b) != want {
		t.Errorf("json:\n%s\nistenen\n%s", b, want)
	}
	// An empty report returns the lists as [] not null.
	e, _ := json.Marshal([]any{(Report{}).PaymentsJSON(), (Report{}).ProductsJSON(), GroupsJSON(nil, nil)})
	if string(e) != "[[],[],[]]" {
		t.Errorf("empty: %s", e)
	}
	g := GroupsJSON([]GroupTotal{{ID: "register-1", Receipts: 3, Total: 500, Voided: 1, VoidedTotal: 50}, {ID: "register-2"}},
		func(g GroupTotal) string {
			if g.ID == "register-1" {
				return "Ön kasa"
			}
			return ""
		})
	if g[0].Name != "Ön kasa" || g[1].Name != "register-2" || g[0].VoidedTotalKurus != 50 {
		t.Errorf("gruplar: %+v", g)
	}
}
