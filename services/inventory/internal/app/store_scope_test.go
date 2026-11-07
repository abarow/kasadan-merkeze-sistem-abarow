// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	inventoryv1 "abarow.com/gen/com/abarow/inventory/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/adapters/out/eventproto"
	"abarow.com/services/inventory/internal/adapters/out/postgres"
	"abarow.com/services/inventory/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/inventory/internal/app"
)

func setup(t *testing.T) (*app.Service, *postgres.Store) {
	t.Helper()
	ctx := context.Background()
	store := postgrestest.Open(t).Shop("store-1")
	n := 0
	newID := func() string { n++; return fmt.Sprintf("id-%d", n) }
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	svc := app.NewService(store, eventproto.Encoder{StoreID: "store-1", NewID: newID}, app.Config{Here: "store-1", Locations: store}, newID, func() time.Time { at = at.Add(time.Minute); return at })
	for _, p := range []stock.Product{
		{ID: "ekmek", Name: "Ekmek 250 g", Unit: stock.Piece, Barcodes: []string{"8690000000012"}},
		{ID: "domates", Name: "Domates", Unit: stock.Kg, Barcodes: []string{"2700001"}},
	} {
		if err := svc.ApplyProduct(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return svc, store
}

// stockItems returns the stock list with this store's quantities.
func stockItems(ctx context.Context, svc *app.Service, q string) ([]stock.Item, error) {
	_, rows, err := svc.Overview(ctx, q, func(string) bool { return true })
	var out []stock.Item
	for _, r := range rows {
		out = append(out, stock.Item{Product: r.Product, Qty: r.Qty[svc.Here()]})
	}
	return out, err
}

func qty(t *testing.T, svc *app.Service, barcode string) money.Qty {
	t.Helper()
	it, err := svc.ByBarcode(context.Background(), barcode)
	if err != nil {
		t.Fatal(err)
	}
	return it.Qty
}

// Goods receipt increases the stock, a sale decreases it (once even if the same sale comes
// twice), stock can go negative, a count corrects the difference; every movement is written to
// the outbox as an event that will go to headquarters.
func TestStockFlow(t *testing.T) {
	ctx := context.Background()
	svc, store := setup(t)

	if _, err := svc.Receive(ctx, "Un Değirmeni", "IRS-1", "ali", []stock.Line{{ProductID: "ekmek", Qty: 10000}, {ProductID: "domates", Qty: 5250}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Receive(ctx, "un değirmeni", "IRS-1", "ali", []stock.Line{{ProductID: "ekmek", Qty: 1000}}); !errors.Is(err, stock.ErrDuplicateReceipt) {
		t.Errorf("same delivery note a second time: %v", err)
	}
	sale := []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 12000}, {ProductID: "domates", Name: "Domates", Qty: 1250}}
	saleAt := time.Date(2026, 10, 29, 9, 1, 30, 0, time.UTC) // after the goods receipt, before the count
	for range 2 {
		if err := svc.ApplySale(ctx, "s1", "ayse", saleAt, sale); err != nil {
			t.Fatal(err)
		}
	}
	if q := qty(t, svc, "8690000000012"); q != -2000 {
		t.Errorf("ekmek %v, istenen -2 (eksi stok)", q)
	}
	if q := qty(t, svc, "2700001"); q != 4000 {
		t.Errorf("domates %v, istenen 4,000 kg", q)
	}

	m, err := svc.Count(ctx, "SAYIM-1", "ali", []stock.Line{{ProductID: "ekmek", Counted: 3000}})
	if err != nil || m.Lines[0].Qty != 5000 || m.Lines[0].System != -2000 {
		t.Fatalf("count: %+v %v", m, err)
	}
	if q := qty(t, svc, "8690000000012"); q != 3000 {
		t.Errorf("bread after the count %v, want 3", q)
	}

	hist, err := svc.History(ctx, "store-1", "ekmek")
	if err != nil || len(hist) != 3 || hist[0].Kind != stock.Count || hist[1].Kind != stock.Sale || hist[2].Kind != stock.Receipt {
		t.Fatalf("history: %+v %v", hist, err)
	}

	// Goods receipt, sale (once) and count: three events.
	pending, err := store.Pending(ctx, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("outbox: %d %v", len(pending), err)
	}
	var ev inventoryv1.StockMoved
	if err := proto.Unmarshal(pending[1].Payload, &ev); err != nil {
		t.Fatal(err)
	}
	if pending[1].Subject != "store.stock.moved.v1" || ev.GetKind() != inventoryv1.MovementKind_MOVEMENT_KIND_SALE ||
		ev.GetLocationId() != "store-1" || ev.GetMovementId() != "sale:s1" || ev.GetLines()[0].GetQtyMilli() != -12000 {
		t.Errorf("sale event: %s %v", pending[1].Subject, &ev)
	}
}

// In a store not connected to headquarters a product is learned by name from a sale; when the
// catalog arrives its name and unit come from the catalog, and a sale line does not
// change the one in the catalog.
func TestProductsFromSales(t *testing.T) {
	ctx := context.Background()
	svc, _ := setup(t)
	if err := svc.ApplySale(ctx, "s1", "ayse", time.Now(), []stock.Line{{ProductID: "cay", Name: "Çay 1 kg", Qty: 1000}, {ProductID: "ekmek", Name: "eski ad", Qty: 1000}}); err != nil {
		t.Fatal(err)
	}
	items, err := stockItems(ctx, svc, "çay")
	if err != nil || len(items) != 1 || items[0].Name != "Çay 1 kg" || items[0].Qty != -1000 {
		t.Fatalf("product learned from a sale: %+v %v", items, err)
	}
	// A product not in the catalog is counted as weighed (kg) when it is sold in fractions.
	if err := svc.ApplySale(ctx, "s2", "ayse", time.Now(), []stock.Line{{ProductID: "peynir", Name: "Peynir", Qty: 1250}}); err != nil {
		t.Fatal(err)
	}
	if items, err := stockItems(ctx, svc, "peynir"); err != nil || len(items) != 1 || items[0].Unit != stock.Kg {
		t.Errorf("unit of a product sold in fractions: %+v %v", items, err)
	}
	items, err = stockItems(ctx, svc, "8690000000012")
	if err != nil || len(items) != 1 || items[0].Name != "Ekmek 250 g" {
		t.Errorf("name changed in the catalog: %+v %v", items, err)
	}
	if _, err := svc.Receive(ctx, "T", "1", "ali", []stock.Line{{ProductID: "yok", Qty: 1000}}); !errors.Is(err, stock.ErrUnknownProduct) {
		t.Errorf("unknown product: %v", err)
	}
	if _, err := svc.ByBarcode(ctx, "000"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("bilinmeyen barkod: %v", err)
	}
}

// Transfer: what is sent is deducted immediately; a transfer coming down from headquarters is
// on the way until it is received, the received quantity is added to stock, it cannot be received a
// second time. One sent to another store is skipped.
func TestTransfers(t *testing.T) {
	ctx := context.Background()
	svc, store := setup(t)
	if err := svc.ApplyLocations(ctx, []stock.Location{{ID: "headquarters", Name: "Merkez deposu"}, {ID: "store-1", Name: "Mağaza 1"}, {ID: "store-2", Name: "Mağaza 2"}}); err != nil {
		t.Fatal(err)
	}
	locs, err := svc.Locations(ctx)
	if err != nil || len(locs) != 3 || locs[0].ID != "store-1" || locs[1].ID != "store-2" || locs[2].ID != "headquarters" {
		t.Fatalf("places (including itself, by name): %+v %v", locs, err)
	}
	if _, err := svc.Send(ctx, "store-9", "ali", []stock.Line{{ProductID: "ekmek", Qty: 1000}}); !errors.Is(err, stock.ErrUnknownLocation) {
		t.Errorf("bilinmeyen yer: %v", err)
	}
	if _, err := svc.Send(ctx, "store-1", "ali", []stock.Line{{ProductID: "ekmek", Qty: 1000}}); !errors.Is(err, stock.ErrSameLocation) {
		t.Errorf("kendine: %v", err)
	}
	out, err := svc.Send(ctx, "store-2", "ali", []stock.Line{{ProductID: "ekmek", Qty: 2000}})
	if err != nil || out.From != "store-1" || out.To != "store-2" {
		t.Fatalf("send: %+v %v", out, err)
	}
	if q := qty(t, svc, "8690000000012"); q != -2000 {
		t.Errorf("what was sent was not deducted: %v", q)
	}

	other := stock.Transfer{ID: "T-2", From: "headquarters", To: "store-2", By: "depo", At: time.Now(), Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 1000}}}
	in := stock.Transfer{ID: "T-1", From: "headquarters", To: "store-1", By: "depo", At: time.Now(),
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 5000}, {ProductID: "su", Name: "Su 5 L", Qty: 2000}}}
	for _, tr := range []stock.Transfer{other, in, in} {
		if err := svc.ApplyTransferSent(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.Transfers(ctx, func(string) bool { return true })
	if err != nil || len(list) != 2 || list[0].ID != "T-1" || !list[0].Pending() || list[1].ID != out.ID {
		t.Fatalf("transfers (incoming in transit first): %+v %v", list, err)
	}

	m, err := svc.ReceiveTransfer(ctx, "T-1", "ayse", []stock.Line{{ProductID: "ekmek", Qty: 4000}})
	if err != nil || len(m.Lines) != 2 || m.Lines[1].Qty != 0 || m.Counterpart != "headquarters" {
		t.Fatalf("teslim al: %+v %v", m, err)
	}
	if q := qty(t, svc, "8690000000012"); q != 2000 {
		t.Errorf("what was received did not enter: %v", q)
	}
	if _, err := svc.ReceiveTransfer(ctx, "T-1", "ayse", nil); !errors.Is(err, stock.ErrTransferReceived) {
		t.Errorf("ikinci kez: %v", err)
	}
	if _, err := svc.ReceiveTransfer(ctx, "T-2", "ayse", nil); !errors.Is(err, stock.ErrUnknownTransfer) {
		t.Errorf("another store's transfer: %v", err)
	}
	got, err := store.Transfer(ctx, "T-1")
	if err != nil || got.Pending() || got.ReceivedBy != "ayse" || len(got.Received) != 2 || got.Received[0].Qty != 4000 || got.Received[1].Qty != 0 {
		t.Errorf("received record: %+v %v", got, err)
	}

	// Sent and received: two events, with their counterpart places.
	pending, err := store.Pending(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("outbox: %d %v", len(pending), err)
	}
	var sent, recv inventoryv1.StockMoved
	if err := proto.Unmarshal(pending[0].Payload, &sent); err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(pending[1].Payload, &recv); err != nil {
		t.Fatal(err)
	}
	if sent.GetKind() != inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_OUT || sent.GetCounterpart() != "store-2" || sent.GetRef() != out.ID ||
		recv.GetKind() != inventoryv1.MovementKind_MOVEMENT_KIND_TRANSFER_IN || recv.GetCounterpart() != "headquarters" || recv.GetRef() != "T-1" {
		t.Errorf("olaylar: %v / %v", &sent, &recv)
	}
}
