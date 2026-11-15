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
	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
)

func TestSaveAndGet(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.Get(ctx, "s1"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("error = %v, want %v", err, app.ErrNotFound)
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
		t.Errorf("record read %+v, want %+v", *got, want)
	}
	if err := st.Save(ctx, want); err == nil {
		t.Error("a second record for the same sale was accepted")
	}
}
