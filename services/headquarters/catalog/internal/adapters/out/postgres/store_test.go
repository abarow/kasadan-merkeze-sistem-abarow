// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/outbox"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// testStore opens an empty database for each test. The tests use the server in
// ABAROW_TEST_POSTGRES; they are skipped if it is not set (see the postgres tests of
// services/headquarters/ingest).
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
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("test veritabanı silinemedi: %v", err)
		}
		admin.Close()
	})
	return store
}

var at = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func product(code, name string, barcodes ...string) domain.Product {
	return domain.Product{Code: code, Name: name, Unit: domain.Piece, TaxRate: 100, Barcodes: barcodes,
		Prices: []domain.Price{{Amount: 1500}}, UpdatedBy: "ayse", UpdatedAt: at, Version: 1}
}

func msg(id string) outbox.Message {
	return outbox.Message{ID: id, Type: "t", Subject: "catalog.product.x", Payload: []byte{1}, ContentType: "application/protobuf", OccurredAt: at}
}

func change(code string) app.Change {
	return app.Change{Code: code, At: at, By: "ayse", Action: app.ActionCreated, Detail: "eklendi"}
}

func TestProducts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	bread := product("ekmek", "Ekmek 250 g", "8690000000012", "8690000000099")
	bread.Prices = append(bread.Prices, domain.Price{Amount: 1650, From: at.Add(72 * time.Hour)})
	if err := s.Create(ctx, bread, change("ekmek"), msg("m1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "ekmek")
	if err != nil || !reflect.DeepEqual(got, bread) {
		t.Fatalf("ürün %+v %v; %+v olmalı", got, err, bread)
	}
	if err := s.Create(ctx, product("ekmek", "X"), change("ekmek"), msg("m2")); !errors.Is(err, domain.ErrExists) {
		t.Errorf("aynı kod: %v", err)
	}
	if err := s.Create(ctx, product("simit", "Simit", "8690000000012"), change("simit"), msg("m3")); !errors.Is(err, domain.ErrBarcodeTaken) {
		t.Errorf("başkasının barkodu: %v", err)
	}
	if _, err := s.Get(ctx, "simit"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("yarım kalan ürün yazıldı: %v", err)
	}

	// Update: the barcodes and the price schedule are renewed; an update with an old version is rejected.
	bread.Name, bread.Barcodes, bread.Prices, bread.Version, bread.UpdatedBy = "Ekmek 300 g", []string{"8690000000012"}, []domain.Price{{Amount: 1700}}, 2, "mehmet"
	if err := s.Update(ctx, bread, change("ekmek"), msg("m4")); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "ekmek"); !reflect.DeepEqual(got, bread) {
		t.Errorf("güncellenen ürün %+v", got)
	}
	if err := s.Update(ctx, bread, change("ekmek"), msg("m5")); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("eski sürüm: %v", err)
	}
	nope := product("yok", "Yok")
	nope.Version = 2
	if err := s.Update(ctx, nope, change("yok"), msg("m6")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("olmayan ürün: %v", err)
	}

	// Search: name (Turkish lower case), code or barcode; Turkish alphabet order.
	for _, p := range []domain.Product{product("cay", "Çay 500 g"), product("cips", "Cips"), product("sut", "Süt 1 L"), product("su", "Su 5 L")} {
		if err := s.Create(ctx, p, change(p.Code), msg("m-"+p.Code)); err != nil {
			t.Fatal(err)
		}
	}
	names := func(q string) []string {
		list, err := s.List(ctx, q, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range list {
			out = append(out, p.Code)
		}
		return out
	}
	if got := names(""); !reflect.DeepEqual(got, []string{"cips", "cay", "ekmek", "su", "sut"}) {
		t.Errorf("sıra %v (Cips, Çay, Ekmek, Su, Süt olmalı)", got)
	}
	if got := names("ÇAY"); !reflect.DeepEqual(got, []string{"cay"}) {
		t.Errorf("Türkçe büyük harfle arama %v", got)
	}
	if got := names("8690000000012"); !reflect.DeepEqual(got, []string{"ekmek"}) {
		t.Errorf("barkodla arama %v", got)
	}
	if got := names("SUT"); !reflect.DeepEqual(got, []string{"sut"}) {
		t.Errorf("kodla arama %v", got)
	}

	// Change records and the outbox.
	history, err := s.History(ctx, "ekmek", 10)
	if err != nil || len(history) != 2 || history[0].Detail != "eklendi" || !history[0].At.Equal(at) {
		t.Errorf("geçmiş %+v %v", history, err)
	}
	pending, err := s.Pending(ctx, 100)
	if err != nil || len(pending) != 6 || pending[0].ID != "m1" || pending[1].ID != "m4" || pending[0].StoreID != "headquarters" {
		t.Fatalf("bekleyen mesajlar %+v %v", pending, err)
	}
	if err := s.MarkPublished(ctx, pending[0].Seq, at); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountPending(ctx); err != nil || n != 5 {
		t.Errorf("%d bekleyen; 5 olmalı (%v)", n, err)
	}
}

// Menu details and image: the options are stored; the image is written with a version,
// and after deletion it is gone.
func TestMenuAndImage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	meatball := product("kofte", "Köfte")
	meatball.Category, meatball.MenuOrder, meatball.Station = "Ana yemekler", 20, menu.Kitchen
	meatball.Options = []menu.Group{{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az"}, {ID: "iyi", Name: "iyi", PriceDelta: 500}}}}
	if err := s.Create(ctx, meatball, change("kofte"), msg("m1")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, "kofte"); err != nil || !reflect.DeepEqual(got, meatball) {
		t.Fatalf("menü ürünü %+v %v; %+v olmalı", got, err, meatball)
	}
	meatball.ImageSHA256, meatball.ImageType, meatball.Version = "abc", "image/png", 2
	img := domain.Image{Code: "kofte", SHA256: "abc", ContentType: "image/png", Data: []byte{1, 2, 3}}
	if err := s.SetImage(ctx, meatball, img, change("kofte"), []outbox.Message{msg("m2"), msg("m3")}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Image(ctx, "kofte"); err != nil || !reflect.DeepEqual(got, img) {
		t.Errorf("görsel %+v %v", got, err)
	}
	if err := s.SetImage(ctx, meatball, img, change("kofte"), nil); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("eski sürümle görsel: %v", err)
	}
	if n, _ := s.CountPending(ctx); n != 3 {
		t.Errorf("outbox: %d mesaj", n)
	}
	meatball.ImageSHA256, meatball.ImageType, meatball.Version = "", "", 3
	if err := s.SetImage(ctx, meatball, domain.Image{Code: "kofte"}, change("kofte"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Image(ctx, "kofte"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("silinen görsel: %v", err)
	}
}
