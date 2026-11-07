// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
)

func TestSaveAndGet(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "payment.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.Get(ctx, "s1"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("hata = %v, istenen %v", err, app.ErrNotFound)
	}
	want := domain.Charge{
		SaleID:   "s1",
		Payments: []domain.Payment{{Method: domain.Card, Amount: 10000, Ref: "POS-1"}, {Method: domain.Cash, Amount: 3000}},
		At:       time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC),
	}
	if err := st.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("okunan kayıt %+v, istenen %+v", *got, want)
	}
	if err := st.Save(ctx, want); err == nil {
		t.Error("aynı satış için ikinci kayıt kabul edildi")
	}
}
