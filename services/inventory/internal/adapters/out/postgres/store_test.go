// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/adapters/out/eventproto"
	"abarow.com/services/inventory/internal/app"
)

func newService(store *Store) *app.Service {
	n := 0
	newID := func() string { n++; return fmt.Sprintf("id-%d", n) }
	stores := []stock.Location{{ID: "store-1", Name: "Mağaza 1"}, {ID: "store-2", Name: "Mağaza 2"}}
	return app.NewService(store, eventproto.Encoder{NewID: newID, Now: time.Now}, app.Config{Here: app.Warehouse, Headquarters: true, Stores: stores}, newID, nil)
}

// testStore opens the stock on an empty test database.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("ABAROW_TEST_POSTGRES yok; PostgreSQL testleri atlandı")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abarow_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	connStr := stdlib.RegisterConnConfig(cfg)
	store, err := Open(ctx, connStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		stdlib.UnregisterConnConfig(connStr)
		admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return store
}

func TestWarehouseAndStores(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	svc := newService(store)
	for _, p := range []stock.Product{
		{ID: "ekmek", Name: "Ekmek 250 g", Unit: stock.Piece, Barcodes: []string{"8690000000012"}},
		{ID: "domates", Name: "Domates", Unit: stock.Kg},
	} {
		if err := svc.ApplyProduct(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Receive(ctx, "Un Değirmeni", "IRS-1", "Merkez", []stock.Line{{ProductID: "ekmek", Qty: 100000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Receive(ctx, "un değirmeni", "IRS-1", "Merkez", []stock.Line{{ProductID: "ekmek", Qty: 1000}}); !errors.Is(err, stock.ErrDuplicateReceipt) {
		t.Errorf("aynı irsaliye: %v", err)
	}
	m, err := svc.Count(ctx, "D-1", "Merkez", []stock.Line{{ProductID: "ekmek", Counted: 98000}})
	if err != nil || m.Lines[0].Qty != -2000 {
		t.Fatalf("sayım: %+v %v", m, err)
	}
	// Even if the store's movement comes twice it is counted once; the same delivery note
	// is separate at another place.
	sale := stock.Movement{ID: "sale:s1", Kind: stock.Sale, Ref: "s1", By: "ayse", At: time.Now(),
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: -3000}, {ProductID: "cay", Name: "Çay 1 kg", Qty: -1250}}}
	for range 2 {
		if err := svc.ApplyStoreMovement(ctx, "store-1", sale); err != nil {
			t.Fatal(err)
		}
	}
	rec := stock.Movement{ID: "r1", Kind: stock.Receipt, Ref: "IRS-1", Supplier: "Un Değirmeni", By: "Müdür", At: time.Now(),
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 5000}}}
	if err := svc.ApplyStoreMovement(ctx, "store-2", rec); err != nil {
		t.Fatalf("başka mağazada aynı irsaliye: %v", err)
	}

	all := func(string) bool { return true }
	locs, rows, err := svc.Overview(ctx, "", all)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(locs) != "[headquarters store-1 store-2]" {
		t.Errorf("yerler %v", locs)
	}
	byID := map[string]app.Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if q := byID["ekmek"].Qty; q["headquarters"] != 98000 || q["store-1"] != -3000 || q["store-2"] != 5000 {
		t.Errorf("ekmek: %v", q)
	}
	if c := byID["cay"]; c.Name != "Çay 1 kg" || c.Unit != stock.Kg || c.Qty["store-1"] != -1250 {
		t.Errorf("mağazadan öğrenilen ürün: %+v", c)
	}
	// A person who sees only store-1 does not see store-2; the headquarters warehouse is always visible.
	locs, rows, _ = svc.Overview(ctx, "EKMEK", func(l string) bool { return l == "store-1" })
	if fmt.Sprint(locs) != "[headquarters store-1]" || len(rows) != 1 || len(rows[0].Qty) != 2 {
		t.Errorf("süzülmüş: %v %+v", locs, rows)
	}
	if it, err := svc.ByBarcode(ctx, "8690000000012"); err != nil || it.Qty != 98000 {
		t.Errorf("barkod: %+v %v", it, err)
	}
	if h, err := svc.History(ctx, "store-1", "ekmek"); err != nil || len(h) != 1 || h[0].Lines[0].Qty != -3000 {
		t.Errorf("geçmiş: %+v %v", h, err)
	}
}

// Transfer: what is sent from the warehouse is deducted from the warehouse and its event is written to the outbox;
// what comes from a store to the warehouse is on the way, when the warehouse receives it the received quantity is added;
// when the store receives, the transfer is closed; a repeated movement counts once.
func TestTransfers(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	svc := newService(store)
	if err := svc.ApplyProduct(ctx, stock.Product{ID: "ekmek", Name: "Ekmek 250 g", Unit: stock.Piece}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PublishLocations(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Send(ctx, "store-1", "Merkez", []stock.Line{{ProductID: "ekmek", Qty: 10000}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Minute)
	toWarehouse := stock.Movement{ID: "m1", Kind: stock.TransferOut, Ref: "T-9", Counterpart: "headquarters", By: "Müdür", At: at,
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: -3000}}}
	for range 2 {
		if err := svc.ApplyStoreMovement(ctx, "store-2", toWarehouse); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.Pending(ctx, 10)
	if err != nil || len(pending) != 2 || pending[0].Subject != "transfer.locations.v1" || pending[1].Subject != "transfer.store-1."+out.ID+".sent.v1" {
		t.Fatalf("outbox: %+v %v", pending, err)
	}
	if err := store.MarkPublished(ctx, pending[0].Seq, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n, err := store.CountPending(ctx); err != nil || n != 1 {
		t.Errorf("bekleyen: %d %v", n, err)
	}

	list, err := svc.Transfers(ctx, func(string) bool { return true })
	if err != nil || len(list) != 2 || list[0].ID != "T-9" || !list[0].Pending() || list[1].Lines[0].Qty != 10000 {
		t.Fatalf("transferler: %+v %v", list, err)
	}
	m, err := svc.ReceiveTransfer(ctx, "T-9", "Merkez", []stock.Line{{ProductID: "ekmek", Qty: 2000}})
	if err != nil || m.Counterpart != "store-2" {
		t.Fatalf("depo teslim aldı: %+v %v", m, err)
	}
	if _, err := svc.ReceiveTransfer(ctx, "T-9", "Merkez", nil); !errors.Is(err, stock.ErrTransferReceived) {
		t.Errorf("ikinci kez: %v", err)
	}
	got, err := store.Transfer(ctx, "T-9")
	if err != nil || got.Pending() || got.ReceivedBy != "Merkez" || len(got.Received) != 1 || got.Received[0].Qty != 2000 {
		t.Errorf("teslim alındı: %+v %v", got, err)
	}
	// Warehouse: −10 sent, +2 received.
	q, err := store.Quantities(ctx, "headquarters", []string{"ekmek"})
	if err != nil || q["ekmek"] != -8000 {
		t.Errorf("depo stoğu: %v %v", q, err)
	}

	in := stock.Movement{ID: "m2", Kind: stock.TransferIn, Ref: out.ID, Counterpart: "headquarters", By: "Ayşe", At: at,
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 10000}}}
	if err := svc.ApplyStoreMovement(ctx, "store-1", in); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Transfer(ctx, out.ID); err != nil || got.Pending() || got.ReceivedBy != "Ayşe" {
		t.Errorf("mağaza teslim aldı: %+v %v", got, err)
	}
	if h, err := svc.History(ctx, "store-1", "ekmek"); err != nil || len(h) != 1 || h[0].Counterpart != "headquarters" || h[0].Kind != stock.TransferIn {
		t.Errorf("mağaza geçmişi: %+v %v", h, err)
	}
}

// Store scope (the store server): the stock has a single place, the place list comes from headquarters,
// the outbox messages carry the store id.
func TestShopScope(t *testing.T) {
	ctx := context.Background()
	store := testStore(t).Shop("store-1")
	n := 0
	newID := func() string { n++; return fmt.Sprintf("id-%d", n) }
	svc := app.NewService(store, eventproto.Encoder{StoreID: "store-1", NewID: newID, Now: time.Now},
		app.Config{Here: "store-1", Locations: store}, newID, nil)

	// A store with no stock shows its own place too.
	if places, err := store.Places(ctx); err != nil || fmt.Sprint(places) != "[store-1]" {
		t.Errorf("yerler %v %v", places, err)
	}
	if err := svc.ApplyProduct(ctx, stock.Product{ID: "ekmek", Name: "Ekmek 250 g", Unit: stock.Piece}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Receive(ctx, "Un Değirmeni", "IRS-1", "Müdür", []stock.Line{{ProductID: "ekmek", Qty: 10000}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyLocations(ctx, []stock.Location{{ID: "headquarters", Name: "Merkez"}, {ID: "store-2", Name: "Mağaza 2"}}); err != nil {
		t.Fatal(err)
	}
	if locs, err := svc.Locations(ctx); err != nil || len(locs) != 2 {
		t.Errorf("gönderilebilecek yerler %v %v", locs, err)
	}
	// The last state of the place list is valid.
	if err := svc.ApplyLocations(ctx, []stock.Location{{ID: "headquarters", Name: "Merkez"}}); err != nil {
		t.Fatal(err)
	}
	if locs, _ := svc.Locations(ctx); len(locs) != 1 {
		t.Errorf("son hal: %v", locs)
	}

	pending, err := store.Pending(ctx, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("outbox: %v %v", pending, err)
	}
	for _, p := range pending {
		if p.StoreID != "store-1" {
			t.Errorf("mesajın mağazası %q, istenen store-1", p.StoreID)
		}
	}
	// In the headquarters view the same warehouse reads with the headquarters id.
	if headquartersPending, _ := store.Shop("").Pending(ctx, 10); len(headquartersPending) == 0 || headquartersPending[0].StoreID != app.Warehouse {
		t.Errorf("merkez görünümü: %+v", headquartersPending)
	}
}
