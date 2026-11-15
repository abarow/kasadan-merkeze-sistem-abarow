// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

func TestBasketsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := pgdbtest.URL(t)
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	b := domain.NewBasket("b1", "ayse", now)
	if err := b.Add(domain.Product{ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100}, 2000, false); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(domain.Product{ID: "domates", Name: "Domates", Unit: domain.Kg, UnitPrice: 3990, TaxRate: 100}, 1250, true); err != nil {
		t.Fatal(err)
	}
	b.UpdatedAt = now.Add(time.Microsecond)
	if err := st.Save(ctx, b); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// The service restarted: the basket comes back as it was.
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Get(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Errorf("the basket changed:\n%+v\nwant\n%+v", got, b)
	}

	// The state and the sale of a basket that went to payment are stored.
	if err := got.CheckOut("sale-1"); err != nil {
		t.Fatal(err)
	}
	got.UpdatedAt = now.Add(time.Second)
	if err := st.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	// The last changed basket; thanks to the fixed-width time even a 1 ns difference
	// is ordered correctly.
	other := domain.NewBasket("b2", "ayse", now.Add(time.Second-time.Microsecond))
	if err := st.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	latest, err := st.Latest(ctx, "ayse", now)
	if err != nil || latest.ID != "b1" || latest.Status != domain.CheckedOut || latest.SaleID != "sale-1" {
		t.Errorf("last basket: %+v, %v", latest, err)
	}
	if _, err := st.Latest(ctx, "mert", now); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another cashier's basket: %v", err)
	}
	if _, err := st.Latest(ctx, "ayse", now.Add(time.Hour)); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("basket outside the window: %v", err)
	}

	if err := st.DeleteBefore(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "b2"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("the old basket was not deleted: %v", err)
	}
	if _, err := st.Get(ctx, "b1"); err != nil {
		t.Errorf("the new basket was deleted: %v", err)
	}
}
