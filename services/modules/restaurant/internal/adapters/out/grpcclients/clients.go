// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcclients holds the restaurant's driven adapters that connect to the core
// services (catalog, sales, device, staff) over gRPC.
package grpcclients

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"
	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

const timeout = 5 * time.Second

// Catalog implements app.Menu with the catalog service.
type Catalog struct {
	c catalogv1.CatalogServiceClient
}

var _ app.Menu = Catalog{}

// NewCatalog returns a client over the given connection.
func NewCatalog(conn grpc.ClientConnInterface) Catalog {
	return Catalog{c: catalogv1.NewCatalogServiceClient(conn)}
}

func (c Catalog) Menu(ctx context.Context) ([]app.MenuItem, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.c.ListMenu(ctx, &catalogv1.ListMenuRequest{})
	if err != nil {
		return nil, service.RemoteErrorFrom("catalog", err)
	}
	out := make([]app.MenuItem, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		out = append(out, app.MenuItem{Product: product(p), Category: p.GetCategory(), ImageSHA256: p.GetImageSha256()})
	}
	return out, nil
}

func (c Catalog) Product(ctx context.Context, id string) (domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.c.GetProduct(ctx, &catalogv1.GetProductRequest{ProductId: id})
	switch status.Code(err) {
	case codes.OK:
		return product(resp.GetProduct()), nil
	case codes.NotFound, codes.FailedPrecondition:
		return domain.Product{}, fmt.Errorf("%w: %s", app.ErrProductNotFound, id)
	}
	return domain.Product{}, service.RemoteErrorFrom("catalog", err)
}

func (c Catalog) Image(ctx context.Context, id string) (app.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.c.GetImage(ctx, &catalogv1.GetImageRequest{ProductId: id})
	if status.Code(err) == codes.NotFound {
		return app.Image{}, fmt.Errorf("%w: görsel", domain.ErrNotFound)
	}
	if err != nil {
		return app.Image{}, service.RemoteErrorFrom("catalog", err)
	}
	return app.Image{SHA256: resp.GetSha256(), ContentType: resp.GetContentType(), Data: resp.GetData()}, nil
}

func product(p *catalogv1.Product) domain.Product {
	out := domain.Product{
		ID: p.GetId(), Name: p.GetName(), UnitPrice: money.Money(p.GetUnitPriceKurus()), TaxRate: money.Rate(p.GetTaxRateBp()),
		Station: p.GetStation(), Weighed: p.GetUnit() == catalogv1.Unit_UNIT_KG,
	}
	for _, g := range p.GetOptionGroups() {
		group := menu.Group{ID: g.GetId(), Name: g.GetName(), Min: int(g.GetMin()), Max: int(g.GetMax())}
		for _, o := range g.GetOptions() {
			group.Options = append(group.Options, menu.Option{ID: o.GetId(), Name: o.GetName(), PriceDelta: money.Money(o.GetPriceDeltaKurus())})
		}
		out.Options = append(out.Options, group)
	}
	return out
}

// Sales, app.Sales'i sales servisiyle uygular.
type Sales struct{ c salesv1.SalesServiceClient }

var _ app.Sales = Sales{}

// NewSales returns a client over the given connection.
func NewSales(conn grpc.ClientConnInterface) Sales {
	return Sales{c: salesv1.NewSalesServiceClient(conn)}
}

func (s Sales) CreateSale(ctx context.Context, cashierID string, lines []app.SaleLine) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := &salesv1.CreateSaleRequest{CashierId: cashierID}
	for _, l := range lines {
		req.Lines = append(req.Lines, &salesv1.SaleLine{
			ProductId: l.ProductID, Name: l.Name, QtyMilli: int64(money.Units(int64(l.Qty))),
			UnitPriceKurus: int64(l.UnitPrice), TaxRateBp: int64(l.TaxRate),
		})
	}
	resp, err := s.c.CreateSale(ctx, req)
	if err != nil {
		return "", service.RemoteErrorFrom("satış servisi", err)
	}
	return resp.GetSale().GetId(), nil
}

func (s Sales) Status(ctx context.Context, saleID string) (app.SaleStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.c.GetSale(ctx, &salesv1.GetSaleRequest{SaleId: saleID})
	if status.Code(err) == codes.NotFound {
		// If there is no sale the payment counts as never started: the quantities return to the check.
		return app.SaleVoided, nil
	}
	if err != nil {
		return "", service.RemoteErrorFrom("satış servisi", err)
	}
	switch resp.GetSale().GetStatus() {
	case salesv1.SaleStatus_SALE_STATUS_PAID:
		return app.SalePaid, nil
	case salesv1.SaleStatus_SALE_STATUS_VOIDED:
		return app.SaleVoided, nil
	case salesv1.SaleStatus_SALE_STATUS_CHARGED:
		return app.SaleCharged, nil
	}
	return app.SaleOpen, nil
}

// Printer implements app.Printer with the device service.
type Printer struct{ c devicev1.DeviceServiceClient }

var _ app.Printer = Printer{}

// NewPrinter returns a client over the given connection.
func NewPrinter(conn grpc.ClientConnInterface) Printer {
	return Printer{c: devicev1.NewDeviceServiceClient(conn)}
}

func (p Printer) Columns(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := p.c.GetStatus(ctx, &devicev1.GetStatusRequest{})
	if err != nil {
		return 0, service.RemoteErrorFrom("yazıcı", err)
	}
	return int(resp.GetColumns()), nil
}

func (p Printer) Print(ctx context.Context, station, title string, lines []string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := p.c.Print(ctx, &devicev1.PrintRequest{Title: title, Lines: lines, Station: station})
	if err != nil {
		return service.RemoteErrorFrom("yazıcı", err)
	}
	return nil
}

// Staff implements app.Approver with the manager authority of the staff service.
type Staff struct{ c staffv1.StaffServiceClient }

var _ app.Approver = Staff{}

// NewStaff returns a client over the given connection.
func NewStaff(conn grpc.ClientConnInterface) Staff {
	return Staff{c: staffv1.NewStaffServiceClient(conn)}
}

func (s Staff) authorize(ctx context.Context, cashierID, token string, action staffv1.Action) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := s.c.Authorize(ctx, &staffv1.AuthorizeRequest{CashierId: cashierID, ApprovalToken: token, Action: action})
	if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
		return "", fmt.Errorf("%w: %s", app.ErrNotAuthorized, status.Convert(err).Message())
	}
	if err != nil {
		return "", service.RemoteErrorFrom("personel servisi", err)
	}
	return resp.GetManagerId(), nil
}

func (s Staff) VoidLine(ctx context.Context, cashierID, token string) (string, error) {
	return s.authorize(ctx, cashierID, token, staffv1.Action_ACTION_VOID_ORDER_LINE)
}

func (s Staff) EditFloor(ctx context.Context, cashierID, token string) (string, error) {
	return s.authorize(ctx, cashierID, token, staffv1.Action_ACTION_EDIT_FLOOR)
}
