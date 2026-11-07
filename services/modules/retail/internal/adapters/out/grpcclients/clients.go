// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcclients holds the driven adapters that connect to the core services
// (catalog, sales, device) over gRPC.
package grpcclients

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"
	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

const timeout = 5 * time.Second

// Catalog implements app.Products with the catalog service.
type Catalog struct {
	c catalogv1.CatalogServiceClient
}

var _ app.Products = Catalog{}

// NewCatalog returns a client over the given connection.
func NewCatalog(conn grpc.ClientConnInterface) Catalog {
	return Catalog{c: catalogv1.NewCatalogServiceClient(conn)}
}

func (c Catalog) ByBarcode(ctx context.Context, barcode string) (domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.c.GetByBarcode(ctx, &catalogv1.GetByBarcodeRequest{Barcode: barcode})
	if status.Code(err) == codes.NotFound {
		return domain.Product{}, app.ErrProductNotFound
	}
	if err != nil {
		return domain.Product{}, service.RemoteErrorFrom("catalog", err)
	}
	return productFromProto(resp.GetProduct()), nil
}

func (c Catalog) Search(ctx context.Context, query string, limit int) ([]domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.c.SearchProducts(ctx, &catalogv1.SearchProductsRequest{Query: query, Limit: int32(limit)})
	if err != nil {
		return nil, service.RemoteErrorFrom("catalog", err)
	}
	out := make([]domain.Product, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		out = append(out, productFromProto(p))
	}
	return out, nil
}

func productFromProto(p *catalogv1.Product) domain.Product {
	unit := domain.Piece
	if p.GetUnit() == catalogv1.Unit_UNIT_KG {
		unit = domain.Kg
	}
	var barcode string
	if len(p.GetBarcodes()) > 0 {
		barcode = p.GetBarcodes()[0]
	}
	return domain.Product{
		ID:        p.GetId(),
		Name:      p.GetName(),
		Barcode:   barcode,
		Unit:      unit,
		UnitPrice: money.Money(p.GetUnitPriceKurus()),
		TaxRate:   money.Rate(p.GetTaxRateBp()),
	}
}

// Sales, app.Sales'i sales servisiyle uygular.
type Sales struct{ c salesv1.SalesServiceClient }

var _ app.Sales = Sales{}

// NewSales returns a client over the given connection.
func NewSales(conn grpc.ClientConnInterface) Sales {
	return Sales{c: salesv1.NewSalesServiceClient(conn)}
}

func (s Sales) CreateSale(ctx context.Context, cashierID string, lines []domain.Line) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := &salesv1.CreateSaleRequest{CashierId: cashierID}
	for _, l := range lines {
		req.Lines = append(req.Lines, &salesv1.SaleLine{
			ProductId:      l.ProductID,
			Barcode:        l.Barcode,
			Name:           l.Name,
			QtyMilli:       int64(l.Qty),
			UnitPriceKurus: int64(l.UnitPrice),
			TaxRateBp:      int64(l.TaxRate),
		})
	}
	resp, err := s.c.CreateSale(ctx, req)
	if err != nil {
		return "", service.RemoteErrorFrom("satış servisi", err)
	}
	return resp.GetSale().GetId(), nil
}

// Scale, app.Scale'i device servisinin terazisiyle uygular.
type Scale struct {
	c devicev1.DeviceServiceClient
}

var _ app.Scale = Scale{}

// NewScale returns a client over the given connection.
func NewScale(conn grpc.ClientConnInterface) Scale {
	return Scale{c: devicev1.NewDeviceServiceClient(conn)}
}

// weighTimeout is the longest time for a weighing: device waits up to
// 3 seconds for the scale to settle.
const weighTimeout = 6 * time.Second

func (s Scale) Weigh(ctx context.Context) (money.Qty, error) {
	ctx, cancel := context.WithTimeout(ctx, weighTimeout)
	defer cancel()
	resp, err := s.c.ReadWeight(ctx, &devicev1.ReadWeightRequest{})
	switch status.Code(err) {
	case codes.OK:
		return money.Qty(resp.GetGrams()), nil
	case codes.NotFound, codes.Unimplemented:
		// No scale is installed, or an old device that does not know the scale.
		return 0, app.ErrNoScale
	}
	return 0, service.RemoteErrorFrom("terazi", err)
}
