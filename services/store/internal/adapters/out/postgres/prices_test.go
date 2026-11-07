// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/store/internal/adapters/out/priceproto"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

func TestStorePrices(t *testing.T) {
	ctx := context.Background()
	st := postgrestest.Open(t)
	ist, _ := time.LoadLocation("Europe/Istanbul")
	n := 0
	enc := priceproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }}
	prices := app.NewPrices(st, st, enc, ist)
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	prices.SetClock(func() time.Time { return now })

	// The products coming from headquarters' catalog.
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	for _, p := range []domain.CatalogProduct{
		{ID: "ekmek", Name: "Ekmek 250 g", Unit: "piece", Barcodes: []string{"8690000000012"},
			HeadquartersPrices: []domain.PricePoint{{Amount: 1500}, {Amount: 1650, From: monday}}},
		{ID: "cay", Name: "Çay 1 kg", Unit: "piece", HeadquartersPrices: []domain.PricePoint{{Amount: 24990}}},
		{ID: "cips", Name: "Cips", Unit: "piece", HeadquartersPrices: []domain.PricePoint{{Amount: 2500}}},
	} {
		if err := prices.ApplyCatalog(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	list, err := prices.List(ctx, "")
	if err != nil || len(list) != 3 || list[0].Product.ID != "cips" || list[1].Product.ID != "cay" || list[2].Product.ID != "ekmek" {
		t.Fatalf("liste (Türk alfabesi sırası: Cips, Çay, Ekmek) %+v %v", list, err)
	}
	if list[2].Headquarters != 1500 || list[2].HasStore || len(list[2].HeadquartersUpcoming) != 1 {
		t.Errorf("ekmek %+v", list[2])
	}
	for q, want := range map[string]string{"EKMEK": "ekmek", "8690000000012": "ekmek", "cay": "cay"} {
		if got, _ := prices.List(ctx, q); len(got) != 1 || got[0].Product.ID != want {
			t.Errorf("arama %q: %+v", q, got)
		}
	}

	// An immediately valid store price; removal at a future date.
	pp, err := prices.SetPrice(ctx, "mağaza", "ekmek", 1800, time.Time{})
	if err != nil || !pp.HasStore || pp.Store != 1800 || pp.Headquarters != 1500 {
		t.Fatalf("mağaza fiyatı %+v %v", pp, err)
	}
	pp, err = prices.Clear(ctx, "mağaza", "ekmek", monday)
	if err != nil || pp.Store != 1800 || len(pp.StoreUpcoming) != 1 || !pp.StoreUpcoming[0].Clear {
		t.Fatalf("ileri tarihli kaldırma %+v %v", pp, err)
	}
	if _, err := prices.SetPrice(ctx, "mağaza", "yok", 1, time.Time{}); !errors.Is(err, domain.ErrProductNotFound) {
		t.Errorf("olmayan ürün: %v", err)
	}
	if _, err := prices.SetPrice(ctx, "mağaza", "ekmek", -1, time.Time{}); !errors.Is(err, domain.ErrInvalidPrice) {
		t.Errorf("negatif fiyat: %v", err)
	}
	// The same schedule when reopened.
	pp, history, err := prices.Get(ctx, "ekmek")
	if err != nil || pp.Store != 1800 || len(pp.StorePoints) != 2 || len(history) != 2 ||
		history[0].Detail != "mağaza fiyatı kaldırıldı, merkez fiyatı geçerli, 02.11.2026 00:00 itibarıyla" ||
		history[1].Detail != "mağaza fiyatı 18,00 TL (hemen)" || history[1].By != "mağaza" {
		t.Errorf("ürün %+v %+v %v", pp, history, err)
	}
	// Pazartesi gelince istisna kalkar.
	now = monday
	if got, _ := prices.List(ctx, "ekmek"); got[0].HasStore || got[0].Headquarters != 1650 {
		t.Errorf("pazartesi %+v", got[0])
	}
	now = monday.Add(-time.Hour)
	if _, err := prices.Cancel(ctx, "mağaza", "ekmek", monday); err != nil {
		t.Fatal(err)
	}

	// Every change is two messages: the store setting to the registers, the event to headquarters.
	pending, err := st.Pending(ctx, 100)
	if err != nil || len(pending) != 6 {
		t.Fatalf("bekleyen %d mesaj; 6 olmalı (%v)", len(pending), err)
	}
	if pending[0].Subject != "config.price.ekmek" || pending[1].Subject != "store.price.changed.v1" || pending[0].StoreID != "store-1" {
		t.Errorf("konular %s %s", pending[0].Subject, pending[1].Subject)
	}
	var sp catalogv1.StorePrices
	if err := proto.Unmarshal(pending[4].Payload, &sp); err != nil {
		t.Fatal(err)
	}
	if sp.GetProductId() != "ekmek" || sp.GetProductName() != "Ekmek 250 g" || sp.GetStoreId() != "store-1" || len(sp.GetPrices()) != 1 ||
		sp.GetPrices()[0].GetUnitPriceKurus() != 1800 || sp.GetPrices()[0].GetFrom() != nil || sp.GetUpdatedBy() != "mağaza" {
		t.Errorf("son mağaza ayarı %v", &sp)
	}
	if err := st.MarkPublished(ctx, pending[0].Seq, now); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.CountPending(ctx); c != 5 {
		t.Errorf("%d bekleyen; 5 olmalı", c)
	}
}
