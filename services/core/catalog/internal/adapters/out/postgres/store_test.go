// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

var products = []domain.Product{
	{ID: "ekmek", Name: "Ekmek 250 g", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100, Barcodes: []string{"8690000000012"}, QuickKey: true},
	{ID: "sampuan", Name: "ŞAMPUAN 500 ml", Unit: domain.Piece, UnitPrice: 8990, TaxRate: 2000, Barcodes: []string{"8690000000029", "8690000000036"}},
	{ID: "domates", Name: "Domates", Unit: domain.Kg, UnitPrice: 4990, TaxRate: 100, Barcodes: []string{"2700001"}, QuickKey: true},
	{ID: "isik", Name: "IŞIK Ampul", Unit: domain.Piece, UnitPrice: 4500, TaxRate: 2000, Barcodes: []string{"8690000000043"}},
}

func openSeeded(t *testing.T) (*Store, *app.Service) {
	t.Helper()
	st, err := Open(context.Background(), pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.NewService(st)
	if err := svc.Seed(context.Background(), products); err != nil {
		t.Fatal(err)
	}
	return st, svc
}

func TestByBarcode(t *testing.T) {
	_, svc := openSeeded(t)
	ctx := context.Background()
	got, err := svc.GetByBarcode(ctx, " 8690000000036 ")
	if err != nil {
		t.Fatal(err)
	}
	want := products[1]
	want.Prices = []domain.Price{{Source: domain.HeadquartersPrice, Amount: 8990}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("product %+v, want %+v", got, want)
	}
	if _, err := svc.GetByBarcode(ctx, "0000000000000"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("error = %v, want %v", err, app.ErrNotFound)
	}
}

func TestSearchUsesTurkishCase(t *testing.T) {
	_, svc := openSeeded(t)
	tests := []struct {
		query string
		want  []string
	}{
		{"şampuan", []string{"sampuan"}},
		{"ŞAMP", []string{"sampuan"}},
		{"ışık", []string{"isik"}}, // "IŞIK" becomes "ışık" in Turkish
		{"ekmek", []string{"ekmek"}},
		{"%", nil},                         // a LIKE special character counts as plain text
		{"", []string{"domates", "ekmek"}}, // empty query: the quick keys
	}
	for _, tt := range tests {
		got, err := svc.Search(context.Background(), tt.query, 10)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		if !reflect.DeepEqual(ids, tt.want) {
			t.Errorf("Search(%q) = %v, want %v", tt.query, ids, tt.want)
		}
	}
}

func TestSeedUpdatesExisting(t *testing.T) {
	_, svc := openSeeded(t)
	ctx := context.Background()
	updated := products[0]
	updated.UnitPrice = 1750
	updated.Barcodes = []string{"8690000000050"}
	if err := svc.Seed(ctx, []domain.Product{updated}); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetByBarcode(ctx, "8690000000050"); err != nil || got.UnitPrice != 1750 {
		t.Errorf("updated product: %+v, %v", got, err)
	}
	if _, err := svc.GetByBarcode(ctx, "8690000000012"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("the old barcode still matches: %v", err)
	}
	if err := svc.Seed(ctx, []domain.Product{{ID: "x", Name: "X", Unit: "litre"}}); !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("invalid unit: error = %v, want %v", err, domain.ErrInvalidProduct)
	}
}

func TestTurkishOrder(t *testing.T) {
	ctx := context.Background()
	path := pgdbtest.URL(t)
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(st)
	quick := func(id, name string) domain.Product {
		return domain.Product{ID: id, Name: name, Unit: domain.Piece, UnitPrice: 100, TaxRate: 100, QuickKey: true}
	}
	if err := svc.Seed(ctx, []domain.Product{quick("sut", "Süt"), quick("cay", "Çay"), quick("su", "Su"), quick("cips", "Cips"), quick("seker", "Şeker")}); err != nil {
		t.Fatal(err)
	}
	// As if written before the column was added: the key is filled in at start.
	if _, err := st.db.ExecContext(ctx, `UPDATE products SET sort_name = '' WHERE id = 'cay'`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if st, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	got, err := app.NewService(st).Search(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if want := []string{"cips", "cay", "su", "sut", "seker"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("quick key order %v, want %v (Cips, Çay, Su, Süt, Şeker)", ids, want)
	}
}

// The price schedule is stored; when the product is reloaded the headquarters schedule changes,
// the store prices are kept.
func TestPriceSchedule(t *testing.T) {
	st, _ := openSeeded(t)
	ctx := context.Background()
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	p := products[0]
	p.Prices = []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1500}, {Source: domain.HeadquartersPrice, Amount: 1650, From: monday}}
	if err := st.Upsert(ctx, []domain.Product{p}, domain.OriginSeed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO prices (product_id, source, from_ms, amount, clear) VALUES ('ekmek', 'store', $1, 1800, FALSE)`,
		monday.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := st.ByBarcode(ctx, "8690000000012")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Price{
		{Source: domain.HeadquartersPrice, Amount: 1500},
		{Source: domain.HeadquartersPrice, Amount: 1650, From: monday},
		{Source: domain.StorePrice, Amount: 1800, From: monday.Add(time.Hour)},
	}
	if !reflect.DeepEqual(got.Prices, want) {
		t.Errorf("takvim %+v; %+v expected", got.Prices, want)
	}
	// The headquarters schedule is renewed, the store price stays.
	p.Prices = []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1600}}
	if err := st.Upsert(ctx, []domain.Product{p}, domain.OriginSeed); err != nil {
		t.Fatal(err)
	}
	got, _ = st.ByBarcode(ctx, "8690000000012")
	want = []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1600}, {Source: domain.StorePrice, Amount: 1800, From: monday.Add(time.Hour)}}
	if !reflect.DeepEqual(got.Prices, want) {
		t.Errorf("yenilenen takvim %+v; %+v expected", got.Prices, want)
	}
	// The store price is not written with the product.
	p.Prices = append(p.Prices, domain.Price{Source: domain.StorePrice, Amount: 1, From: monday})
	if err := st.Upsert(ctx, []domain.Product{p}, domain.OriginSeed); err == nil {
		t.Error("the store price was written with the product")
	}
}

// A product that came from headquarters is not changed by the initial file; a product withdrawn from sale
// is not found at the register.
func TestOriginAndDiscontinued(t *testing.T) {
	_, svc := openSeeded(t)
	ctx := context.Background()
	headquarters := products[0]
	headquarters.Name, headquarters.Prices = "Ekmek 300 g", []domain.Price{{Source: domain.HeadquartersPrice, Amount: 1700}}
	if err := svc.Apply(ctx, headquarters); err != nil {
		t.Fatal(err)
	}
	// The old initial file is loaded again (the register restarted).
	if err := svc.Seed(ctx, products); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetByBarcode(ctx, "8690000000012"); err != nil || got.Name != "Ekmek 300 g" || got.UnitPrice != 1700 {
		t.Errorf("headquarters' product was changed by the file: %+v %v", got, err)
	}
	// The other products in the initial file are still updated.
	seedOnly := products[1]
	seedOnly.Name = "ŞAMPUAN 750 ml"
	if err := svc.Seed(ctx, []domain.Product{seedOnly}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetByBarcode(ctx, "8690000000029"); got.Name != "ŞAMPUAN 750 ml" {
		t.Errorf("the product in the file was not updated: %+v", got)
	}

	headquarters.Discontinued = true
	if err := svc.Apply(ctx, headquarters); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetByBarcode(ctx, "8690000000012"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a product taken off sale was found: %v", err)
	}
	if list, _ := svc.Search(ctx, "ekmek", 10); len(list) != 0 {
		t.Errorf("a product taken off sale is in the search: %+v", list)
	}
	if list, _ := svc.Search(ctx, "", 10); len(list) != 1 || list[0].ID != "domates" {
		t.Errorf("quick keys %+v", list)
	}
	headquarters.Discontinued = false
	if err := svc.Apply(ctx, headquarters); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetByBarcode(ctx, "8690000000012"); err != nil {
		t.Errorf("product put on sale again: %v", err)
	}
}

// Menu: the products that have a category, the categories by their lowest-ordered product;
// the options are stored; the image is given only if its digest holds the product's
// (if the new image has not come yet the old one is not shown).
func TestMenuAndImages(t *testing.T) {
	ctx := context.Background()
	st, svc := openSeeded(t)
	img := []byte("\x89PNG\r\n\x1a\n" + "test")
	groups := []menu.Group{{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az"}, {ID: "iyi", Name: "iyi", PriceDelta: 500}}}}
	if err := svc.Seed(ctx, []domain.Product{
		{ID: "kofte", Name: "Köfte", Unit: domain.Piece, UnitPrice: 25000, TaxRate: 1000, Category: "Ana yemekler", MenuOrder: 20, Options: groups,
			ImageSHA256: "eski", ImageType: "image/png", ImageData: img},
		{ID: "corba", Name: "Mercimek çorbası", Unit: domain.Piece, UnitPrice: 9000, TaxRate: 1000, Category: "Çorbalar", MenuOrder: 10},
		{ID: "ayran", Name: "Ayran", Unit: domain.Piece, UnitPrice: 3000, TaxRate: 1000, Category: "İçecekler", MenuOrder: 30, Station: menu.Bar},
		{ID: "pilav", Name: "Pilav", Unit: domain.Piece, UnitPrice: 6000, TaxRate: 1000, Category: "Ana yemekler", MenuOrder: 25},
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Menu(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range items {
		names = append(names, p.Name)
	}
	if want := []string{"Mercimek çorbası", "Köfte", "Pilav", "Ayran"}; !reflect.DeepEqual(names, want) {
		t.Errorf("menu %v, want %v (no grocery products in the menu)", names, want)
	}
	k, err := svc.GetProduct(ctx, "kofte")
	if err != nil || k.UnitPrice != 25000 || !reflect.DeepEqual(k.Options, groups) || items[3].Station != menu.Bar {
		t.Fatalf("product: %+v %v", k, err)
	}
	if got, err := svc.Image(ctx, "kofte"); err != nil || got.SHA256 != "eski" || string(got.Data) != string(img) {
		t.Errorf("image: %+v %v", got, err)
	}
	// The new image digest came from headquarters but the image itself has not come yet.
	k.ImageSHA256 = "yeni"
	if err := st.Upsert(ctx, []domain.Product{k}, domain.OriginHeadquarters); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Image(ctx, "kofte"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("an image that does not match its digest was given: %v", err)
	}
	if err := st.SaveImage(ctx, domain.Image{ProductID: "kofte", SHA256: "yeni", ContentType: "image/png", Data: img}); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Image(ctx, "kofte"); err != nil || got.SHA256 != "yeni" {
		t.Errorf("new image: %+v %v", got, err)
	}
	if _, err := svc.GetProduct(ctx, "yok"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("product that does not exist: %v", err)
	}
}

// An image coming from headquarters is checked from its content: an SVG or a broken file
// is not accepted.
func TestApplyImageChecksContent(t *testing.T) {
	_, svc := openSeeded(t)
	err := svc.ApplyImage(context.Background(), domain.Image{ProductID: "ekmek", Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")})
	if !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("SVG: %v", err)
	}
}
