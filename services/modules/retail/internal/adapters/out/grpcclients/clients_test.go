// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcclients_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"
	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/modules/retail/internal/adapters/out/grpcclients"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

type catalogServer struct {
	catalogv1.UnimplementedCatalogServiceServer
}

func (catalogServer) GetByBarcode(_ context.Context, r *catalogv1.GetByBarcodeRequest) (*catalogv1.GetByBarcodeResponse, error) {
	switch r.GetBarcode() {
	case "8690000000012":
		return &catalogv1.GetByBarcodeResponse{Product: &catalogv1.Product{
			Id: "ekmek", Name: "Ekmek", Barcodes: []string{"8690000000012", "2"}, Unit: catalogv1.Unit_UNIT_PIECE,
			UnitPriceKurus: 1500, TaxRateBp: 100,
		}}, nil
	case "bozuk":
		return nil, status.Error(codes.Internal, "veritabanı bozuk")
	}
	return nil, status.Error(codes.NotFound, "yok")
}

func (catalogServer) SearchProducts(_ context.Context, r *catalogv1.SearchProductsRequest) (*catalogv1.SearchProductsResponse, error) {
	if r.GetQuery() == "bozuk" {
		return nil, status.Error(codes.Internal, "arama bozuk")
	}
	return &catalogv1.SearchProductsResponse{Products: []*catalogv1.Product{
		{Id: "domates", Name: "Domates", Unit: catalogv1.Unit_UNIT_KG, UnitPriceKurus: 4990, TaxRateBp: 100},
	}}, nil
}

type salesServer struct {
	salesv1.UnimplementedSalesServiceServer
	got *salesv1.CreateSaleRequest
}

func (s *salesServer) CreateSale(_ context.Context, r *salesv1.CreateSaleRequest) (*salesv1.CreateSaleResponse, error) {
	s.got = r
	if r.GetCashierId() == "kapali" {
		return nil, status.Error(codes.FailedPrecondition, "açık vardiya yok")
	}
	return &salesv1.CreateSaleResponse{Sale: &salesv1.Sale{Id: "sale-7"}}, nil
}

type deviceServer struct {
	devicev1.UnimplementedDeviceServiceServer
	err   error
	grams int64
}

func (d deviceServer) ReadWeight(context.Context, *devicev1.ReadWeightRequest) (*devicev1.ReadWeightResponse, error) {
	if d.err != nil {
		return nil, d.err
	}
	return &devicev1.ReadWeightResponse{Grams: d.grams}, nil
}

func TestCatalog(t *testing.T) {
	conn := grpctest.Conn(t, func(s *grpc.Server) { catalogv1.RegisterCatalogServiceServer(s, catalogServer{}) })
	c := grpcclients.NewCatalog(conn)
	ctx := context.Background()

	p, err := c.ByBarcode(ctx, "8690000000012")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Product{ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100}
	if p != want {
		t.Errorf("product %+v, want %+v", p, want)
	}
	if _, err := c.ByBarcode(ctx, "yok"); !errors.Is(err, app.ErrProductNotFound) {
		t.Errorf("barcode that does not exist: %v", err)
	}
	if _, err := c.ByBarcode(ctx, "bozuk"); err == nil || errors.Is(err, app.ErrProductNotFound) {
		t.Errorf("a server error was taken as no product: %v", err)
	}

	found, err := c.Search(ctx, "dom", 10)
	if err != nil || len(found) != 1 || found[0].Unit != domain.Kg || found[0].Barcode != "" {
		t.Errorf("arama: %+v %v", found, err)
	}
	if _, err := c.Search(ctx, "bozuk", 10); err == nil {
		t.Error("the search error was not returned")
	}
}

func TestSales(t *testing.T) {
	srv := &salesServer{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { salesv1.RegisterSalesServiceServer(s, srv) })
	sales := grpcclients.NewSales(conn)

	lines := []domain.Line{{ProductID: "ekmek", Barcode: "8690000000012", Name: "Ekmek", Qty: 2000, UnitPrice: 1500, TaxRate: 100}}
	id, err := sales.CreateSale(context.Background(), "ayse", lines)
	if err != nil || id != "sale-7" {
		t.Fatalf("CreateSale: %q %v", id, err)
	}
	got := srv.got.GetLines()
	if srv.got.GetCashierId() != "ayse" || len(got) != 1 || got[0].GetQtyMilli() != 2000 || got[0].GetUnitPriceKurus() != 1500 || got[0].GetTaxRateBp() != 100 {
		t.Errorf("request sent to the server: %v", srv.got)
	}
	if _, err := sales.CreateSale(context.Background(), "kapali", lines); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("server error code: %v", err)
	}
}

func TestScale(t *testing.T) {
	tests := []struct {
		name  string
		dev   deviceServer
		grams int64
		err   error
		other bool
	}{
		{"tartım", deviceServer{grams: 1250}, 1250, nil, false},
		{"terazi yok", deviceServer{err: status.Error(codes.NotFound, "x")}, 0, app.ErrNoScale, false},
		{"desteklenmiyor", deviceServer{err: status.Error(codes.Unimplemented, "x")}, 0, app.ErrNoScale, false},
		{"başka hata", deviceServer{err: status.Error(codes.FailedPrecondition, "terazi oynuyor")}, 0, nil, true},
	}
	for _, tc := range tests {
		conn := grpctest.Conn(t, func(s *grpc.Server) { devicev1.RegisterDeviceServiceServer(s, tc.dev) })
		got, err := grpcclients.NewScale(conn).Weigh(context.Background())
		switch {
		case tc.other:
			if err == nil || status.Code(err) != codes.FailedPrecondition {
				t.Errorf("%s: %v", tc.name, err)
			}
		case !errors.Is(err, tc.err) || int64(got) != tc.grams:
			t.Errorf("%s: %d %v, want %d %v", tc.name, got, err, tc.grams, tc.err)
		}
	}
}
