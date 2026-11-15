// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/pkg/menu"
	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/catalog/internal/adapters/in/grpcserver"
	"abarow.com/services/core/catalog/internal/adapters/out/postgres"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

// png is the smallest content that passes the image check (the PNG signature).
var png = []byte("\x89PNG\r\n\x1a\n\x00\x01\x02")

var pngSum = func() string { _, sum, _ := menu.CheckImage(png); return sum }()

func start(t *testing.T) catalogv1.CatalogServiceClient {
	t.Helper()
	ctx := context.Background()
	st, err := postgres.Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.NewService(st)

	future := time.Now().Add(48 * time.Hour)
	err = svc.Seed(ctx, []domain.Product{
		{ID: "ekmek", Name: "Ekmek", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100, Barcodes: []string{"8690000000012"}, QuickKey: true, ImageSHA256: pngSum, ImageType: "image/png"},
		{ID: "domates", Name: "Domates", Unit: domain.Kg, UnitPrice: 4990, TaxRate: 100, Barcodes: []string{"2700001"}},
		{
			ID: "kofte", Name: "Köfte", Unit: domain.Piece, UnitPrice: 25000, TaxRate: 1000, Category: "Ana yemek", MenuOrder: 1, Station: menu.Kitchen,
			Options: []menu.Group{{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "Az", PriceDelta: 0}, {ID: "cok", Name: "Çok", PriceDelta: 500}}}},
		},
		// A product whose price starts only in the future cannot be sold now.
		{ID: "yeni", Name: "Yeni Ürün", Unit: domain.Piece, TaxRate: 100, Barcodes: []string{"111"}, Prices: []domain.Price{{Source: domain.HeadquartersPrice, Amount: 900, From: future}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The image is given as long as it matches the product's digest.
	if err := svc.ApplyImage(ctx, domain.Image{ProductID: "ekmek", Data: png}); err != nil {
		t.Fatal(err)
	}
	conn := grpctest.Conn(t, func(s *grpc.Server) { catalogv1.RegisterCatalogServiceServer(s, grpcserver.New(svc)) })
	return catalogv1.NewCatalogServiceClient(conn)
}

func TestGetByBarcodeAndProduct(t *testing.T) {
	c := start(t)
	ctx := context.Background()

	r, err := c.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: "8690000000012"})
	if err != nil {
		t.Fatal(err)
	}
	p := r.GetProduct()
	if p.GetId() != "ekmek" || p.GetUnitPriceKurus() != 1500 || p.GetTaxRateBp() != 100 || !p.GetQuickKey() || p.GetUnit() != catalogv1.Unit_UNIT_PIECE {
		t.Errorf("product: %v", p)
	}
	if r, _ := c.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: "2700001"}); r.GetProduct().GetUnit() != catalogv1.Unit_UNIT_KG {
		t.Errorf("kg birimi: %v", r)
	}
	if _, err := c.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: "0000"}); status.Code(err) != codes.NotFound {
		t.Errorf("barcode that does not exist: %v", err)
	}
	if _, err := c.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: "111"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("product whose price is not in effect: %v", err)
	}

	g, err := c.GetProduct(ctx, &catalogv1.GetProductRequest{ProductId: "domates"})
	if err != nil || g.GetProduct().GetName() != "Domates" {
		t.Errorf("GetProduct: %v %v", g, err)
	}
	if _, err := c.GetProduct(ctx, &catalogv1.GetProductRequest{ProductId: "yok"}); status.Code(err) != codes.NotFound {
		t.Errorf("non-existent product: %v", err)
	}
	if _, err := c.GetProduct(ctx, &catalogv1.GetProductRequest{ProductId: "yeni"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("product with no price: %v", err)
	}
}

func TestSearchAndMenu(t *testing.T) {
	c := start(t)
	ctx := context.Background()

	s, err := c.SearchProducts(ctx, &catalogv1.SearchProductsRequest{Query: "ekm", Limit: 10})
	if err != nil || len(s.GetProducts()) != 1 || s.GetProducts()[0].GetId() != "ekmek" {
		t.Errorf("arama: %v %v", s, err)
	}
	m, err := c.ListMenu(ctx, &catalogv1.ListMenuRequest{})
	if err != nil || len(m.GetProducts()) != 1 {
		t.Fatalf("menu (only products that have a category): %v %v", m, err)
	}
	k := m.GetProducts()[0]
	if k.GetId() != "kofte" || k.GetCategory() != "Ana yemek" || k.GetStation() != menu.Kitchen || k.GetMenuOrder() != 1 {
		t.Errorf("menu product: %v", k)
	}
	groups := k.GetOptionGroups()
	if len(groups) != 1 || groups[0].GetMin() != 1 || len(groups[0].GetOptions()) != 2 || groups[0].GetOptions()[1].GetPriceDeltaKurus() != 500 {
		t.Errorf("opsiyonlar: %v", groups)
	}
}

func TestGetImage(t *testing.T) {
	c := start(t)
	ctx := context.Background()

	img, err := c.GetImage(ctx, &catalogv1.GetImageRequest{ProductId: "ekmek"})
	if err != nil || img.GetSha256() != pngSum || img.GetContentType() != "image/png" || !bytes.Equal(img.GetData(), png) {
		t.Errorf("image: %v %v", img, err)
	}
	if _, err := c.GetImage(ctx, &catalogv1.GetImageRequest{ProductId: "domates"}); status.Code(err) != codes.NotFound {
		t.Errorf("product with no image: %v", err)
	}
}
