// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"abarow.com/services/core/catalog/internal/adapters/out/sqlite"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

// pngHeader is the signature of a PNG file (the type is recognized from the content).
const pngHeader = "\x89PNG\r\n\x1a\n"

func TestSeedFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := app.NewService(st)

	path := filepath.Join(dir, "catalog.json")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"id": "ekmek", "name": "Ekmek", "unit": "piece", "unit_price_kurus": 1500, "tax_rate_bp": 100, "barcodes": ["1"],
		"prices": [{"unit_price_kurus": 1650, "from": "2099-11-02T00:00:00+03:00"}]}]`)
	if n, err := seed(ctx, svc, path); err != nil || n != 1 {
		t.Fatalf("load: %d %v", n, err)
	}
	p, err := svc.GetByBarcode(ctx, "1")
	if err != nil || p.UnitPrice != 1500 || len(p.Prices) != 2 || p.Prices[1].Amount != 1650 || p.Prices[1].From.Year() != 2099 ||
		p.Prices[0].Source != domain.HeadquartersPrice {
		t.Errorf("product %+v %v", p, err)
	}

	// The image file is read relative to the JSON's folder and checked from its content.
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "corba.png"), []byte(pngHeader+"veri"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(`[{"id": "corba", "name": "Çorba", "unit": "piece", "unit_price_kurus": 9000, "tax_rate_bp": 1000, "category": "Çorbalar",
		"option_groups": [{"id": "boy", "name": "Porsiyon", "min": 1, "max": 1, "options": [{"id": "tam", "name": "tam"}, {"id": "yarim", "name": "yarım", "price_delta_kurus": -3000}]}],
		"image": "images/corba.png"}]`)
	if _, err := seed(ctx, svc, path); err != nil {
		t.Fatal(err)
	}
	if img, err := svc.Image(ctx, "corba"); err != nil || img.ContentType != "image/png" {
		t.Errorf("image: %+v %v", img, err)
	}
	if p, err := svc.GetProduct(ctx, "corba"); err != nil || p.Category != "Çorbalar" || len(p.Options) != 1 || p.Options[0].Options[1].PriceDelta != -3000 {
		t.Errorf("menu product: %+v %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "corba.png"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := seed(ctx, svc, path); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("invalid image: %v", err)
	}

	write(`[{"id": "ekmek", "name": "Ekmek", "unit": "piece", "unit_price_kurus": 1500, "prices": [{"unit_price_kurus": 1650}]}]`)
	if _, err := seed(ctx, svc, path); err == nil || !strings.Contains(err.Error(), "effective moment") {
		t.Errorf("price with no effective moment: %v", err)
	}
}

// The restaurant trial menu in the repository can be loaded: the options are valid,
// the images are found and their contents are recognized.
func TestRestaurantSeed(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := app.NewService(st)
	n, err := seed(ctx, svc, filepath.Join("..", "..", "..", "..", "..", "deploy", "compose", "seed", "restaurant", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.Menu(ctx)
	if err != nil || len(items) != n || items[0].Category != "Çorbalar" || items[n-1].Category != "İçecekler" {
		t.Fatalf("menu: %d products, %v", len(items), err)
	}
	for _, p := range items {
		if _, err := svc.Image(ctx, p.ID); err != nil {
			t.Errorf("%s: no image: %v", p.ID, err)
		}
	}
}
