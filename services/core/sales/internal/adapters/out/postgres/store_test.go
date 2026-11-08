// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"abarow.com/pkg/money"
	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/app/apptest"
	"abarow.com/services/core/sales/internal/domain"
)

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := pgdbtest.URL(t)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func testSale(t *testing.T, id domain.SaleID) *domain.Sale {
	t.Helper()
	lines := []domain.Line{
		{ProductID: "p1", Barcode: "8690000000011", Name: "Deterjan 1 L", Qty: money.Units(1), UnitPrice: 1299, TaxRate: 2000},
		{ProductID: "p2", Barcode: "2700001012500", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100},
	}
	s, err := domain.NewSale(id, "register-1", "kasiyer-1", lines, apptest.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func save(t *testing.T, st *Store, s *domain.Sale) {
	t.Helper()
	if err := st.InTx(context.Background(), func(tx app.Tx) error { return tx.Save(context.Background(), s) }); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func countOutbox(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenIsIdempotent(t *testing.T) {
	st, path := openStore(t)
	st.Close()
	again, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("ikinci Open: %v", err)
	}
	defer again.Close()
	if err := again.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestSaveAndGet(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	s := testSale(t, "s1")
	save(t, st, s)

	got, err := st.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
		t.Errorf("okunan satış farklı:\n%+v\nistenen\n%+v", got.Snapshot(), s.Snapshot())
	}

	// Updates: first the state with payment taken (charged), then closed (paid).
	payments := []domain.Payment{{Method: domain.PaymentCard, Amount: 10000, Ref: "REF-7"}, {Method: domain.PaymentMealCard, Amount: 2000, Collect: true}, {Method: domain.PaymentCash, Amount: 1000}}
	steps := []func() error{
		func() error { return s.RecordCharge(payments) },
		func() error { return s.MarkPaid(1, "SIM-000001", apptest.Now) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		save(t, st, s)
		got, err = st.Get(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
			t.Errorf("güncellenen satış farklı:\n%+v\nistenen\n%+v", got.Snapshot(), s.Snapshot())
		}
	}
}

func TestGetNotFound(t *testing.T) {
	st, _ := openStore(t)
	if _, err := st.Get(context.Background(), "yok"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("hata = %v, istenen %v", err, app.ErrNotFound)
	}
}

func TestNextReceiptNoIsSequential(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	for want := int64(1); want <= 3; want++ {
		var got int64
		err := st.InTx(ctx, func(tx app.Tx) error {
			var err error
			got, err = tx.NextReceiptNo(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("fiş no = %d, istenen %d", got, want)
		}
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	boom := errors.New("boom")

	err := st.InTx(ctx, func(tx app.Tx) error {
		if _, err := tx.NextReceiptNo(ctx); err != nil {
			return err
		}
		if err := tx.Save(ctx, testSale(t, "s1")); err != nil {
			return err
		}
		msg := app.OutboxMessage{ID: "e1", Type: "t", Subject: "s", StoreID: "m", RegisterID: "k", Payload: []byte("{}"), OccurredAt: apptest.Now}
		if err := tx.AppendOutbox(ctx, []app.OutboxMessage{msg}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("hata = %v, istenen %v", err, boom)
	}
	if _, err := st.Get(ctx, "s1"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("geri alınan satış kalmış: %v", err)
	}
	if n := countOutbox(t, st); n != 0 {
		t.Errorf("outbox'ta %d satır, istenen 0", n)
	}
	var no int64
	_ = st.InTx(ctx, func(tx app.Tx) error {
		var err error
		no, err = tx.NextReceiptNo(ctx)
		return err
	})
	if no != 1 {
		t.Errorf("geri almadan sonra fiş no = %d, istenen 1", no)
	}
}

func TestOutboxPendingAndMarkPublished(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	msgs := []app.OutboxMessage{
		{ID: "e1", Type: "com.abarow.sale.completed.v1", Subject: "sale.completed.v1", StoreID: "m", RegisterID: "k", Payload: []byte{0x0a, 0x02}, ContentType: "application/protobuf", OccurredAt: apptest.Now},
		{ID: "e2", Type: "com.abarow.sale.voided.v1", Subject: "sale.voided.v1", StoreID: "m", RegisterID: "k", Payload: []byte(`{"n":2}`), ContentType: "application/json", OccurredAt: apptest.Now},
	}
	if err := st.InTx(ctx, func(tx app.Tx) error { return tx.AppendOutbox(ctx, msgs) }); err != nil {
		t.Fatal(err)
	}

	pending, err := st.Pending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || !reflect.DeepEqual(pending[0].Message, msgs[0]) || !reflect.DeepEqual(pending[1].Message, msgs[1]) {
		t.Fatalf("bekleyen mesajlar yanlış: %+v", pending)
	}
	if err := st.MarkPublished(ctx, pending[0].Seq, apptest.Now); err != nil {
		t.Fatal(err)
	}
	pending, err = st.Pending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "e2" {
		t.Errorf("işaretlemeden sonra bekleyenler: %+v", pending)
	}
	if n, err := st.CountPending(ctx); err != nil || n != 1 {
		t.Errorf("CountPending = %d, %v; istenen 1", n, err)
	}
}

// TestCheckoutWritesSaleAndOutboxTogether runs the service with real PostgreSQL.
func TestCheckoutWritesSaleAndOutboxTogether(t *testing.T) {
	st, _ := openStore(t)
	ctx := context.Background()
	payments, fiscal := &apptest.Payments{}, &apptest.Fiscal{}
	svc, err := app.NewService(st, payments, fiscal, apptest.Config())
	if err != nil {
		t.Fatal(err)
	}
	sale, err := svc.CreateSale(ctx, "kasiyer-1", testSale(t, "x").Lines())
	if err != nil {
		t.Fatal(err)
	}

	// The fiscal record fails: the payment is recorded on the sale, no receipt number is created;
	// the sale whose receipt could not be issued is announced (sale.charged.v1).
	fiscal.Err = errors.New("yazıcıda kağıt yok")
	cash := []domain.Payment{{Method: domain.PaymentCash, Amount: 20000}}
	if _, err := svc.Checkout(ctx, sale.ID(), cash); err == nil {
		t.Fatal("mali kayıt hatası dönmedi")
	}
	if stored, err := st.Get(ctx, sale.ID()); err != nil || stored.Status() != domain.StatusCharged {
		t.Fatalf("satış charged olmalı: %+v, %v", stored, err)
	}
	if n := countOutbox(t, st); n != 1 {
		t.Errorf("başarısız checkout sonrası outbox'ta %d satır, istenen 1 (sale.charged.v1)", n)
	}

	// Retry: it closes with the same payment; the sale, the receipt number and the event
	// are written together.
	fiscal.Err = nil
	done, err := svc.Checkout(ctx, sale.ID(), cash)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if payments.Calls != 1 {
		t.Errorf("ödeme %d kez alındı, istenen 1", payments.Calls)
	}
	stored, err := st.Get(ctx, done.ID())
	if err != nil || stored.Status() != domain.StatusPaid || stored.ReceiptNo() != 1 {
		t.Fatalf("kaydedilen satış: %+v, %v", stored, err)
	}

	var typ, subject, storeID, registerID string
	var payload []byte
	err = st.db.QueryRow(`SELECT type, subject, store_id, register_id, payload FROM outbox WHERE published_at IS NULL AND subject = 'sale.completed.v1'`).
		Scan(&typ, &subject, &storeID, &registerID, &payload)
	if err != nil {
		t.Fatalf("outbox okunamadı: %v", err)
	}
	if typ != "com.abarow.sale.completed.v1" || subject != "sale.completed.v1" || storeID != "store-1" || registerID != "register-1" {
		t.Errorf("outbox satırı yanlış: %s %s %s %s", typ, subject, storeID, registerID)
	}
	var ev domain.SaleCompleted
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ReceiptNo != 1 || ev.Total != 12537 {
		t.Errorf("payload yanlış: %+v", ev)
	}
}
