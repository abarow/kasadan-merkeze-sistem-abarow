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
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/modules/restaurant/internal/adapters/out/grpcclients"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

type catalogServer struct {
	catalogv1.UnimplementedCatalogServiceServer
}

func (catalogServer) ListMenu(context.Context, *catalogv1.ListMenuRequest) (*catalogv1.ListMenuResponse, error) {
	return &catalogv1.ListMenuResponse{Products: []*catalogv1.Product{{
		Id: "kofte", Name: "Köfte", Category: "Ana yemek", Station: "mutfak", ImageSha256: "abc",
		UnitPriceKurus: 25000, TaxRateBp: 1000,
		OptionGroups: []*catalogv1.OptionGroup{{
			Id: "pisirme", Name: "Pişirme", Min: 1, Max: 1,
			Options: []*catalogv1.Option{{Id: "az", Name: "Az pişmiş", PriceDeltaKurus: 0}, {Id: "ekstra", Name: "Ekstra", PriceDeltaKurus: 500}},
		}},
	}}}, nil
}

func (catalogServer) GetProduct(_ context.Context, r *catalogv1.GetProductRequest) (*catalogv1.GetProductResponse, error) {
	switch r.GetProductId() {
	case "kofte":
		return &catalogv1.GetProductResponse{Product: &catalogv1.Product{Id: "kofte", Name: "Köfte", Unit: catalogv1.Unit_UNIT_KG}}, nil
	case "yurursuz":
		return nil, status.Error(codes.FailedPrecondition, "fiyatı henüz yürürlükte değil")
	case "bozuk":
		return nil, status.Error(codes.Internal, "bozuk")
	}
	return nil, status.Error(codes.NotFound, "yok")
}

func (catalogServer) GetImage(_ context.Context, r *catalogv1.GetImageRequest) (*catalogv1.GetImageResponse, error) {
	switch r.GetProductId() {
	case "kofte":
		return &catalogv1.GetImageResponse{Sha256: "abc", ContentType: "image/png", Data: []byte{1, 2, 3}}, nil
	case "bozuk":
		return nil, status.Error(codes.Internal, "bozuk")
	}
	return nil, status.Error(codes.NotFound, "yok")
}

func TestCatalog(t *testing.T) {
	conn := grpctest.Conn(t, func(s *grpc.Server) { catalogv1.RegisterCatalogServiceServer(s, catalogServer{}) })
	c := grpcclients.NewCatalog(conn)
	ctx := context.Background()

	items, err := c.Menu(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("Menu: %v %v", items, err)
	}
	it := items[0]
	if it.Category != "Ana yemek" || it.ImageSHA256 != "abc" || it.Station != "mutfak" || it.UnitPrice != 25000 || it.TaxRate != 1000 {
		t.Errorf("menu item: %+v", it)
	}
	if len(it.Options) != 1 || it.Options[0].Min != 1 || len(it.Options[0].Options) != 2 || it.Options[0].Options[1].PriceDelta != 500 {
		t.Errorf("option groups: %+v", it.Options)
	}

	p, err := c.Product(ctx, "kofte")
	if err != nil || !p.Weighed {
		t.Errorf("Product: %+v %v", p, err)
	}
	for _, id := range []string{"yok", "yurursuz"} {
		if _, err := c.Product(ctx, id); !errors.Is(err, app.ErrProductNotFound) {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := c.Product(ctx, "bozuk"); err == nil || errors.Is(err, app.ErrProductNotFound) {
		t.Errorf("server error: %v", err)
	}

	img, err := c.Image(ctx, "kofte")
	if err != nil || img.SHA256 != "abc" || img.ContentType != "image/png" || len(img.Data) != 3 {
		t.Errorf("Image: %+v %v", img, err)
	}
	if _, err := c.Image(ctx, "yok"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("image that does not exist: %v", err)
	}
	if _, err := c.Image(ctx, "bozuk"); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("image server error: %v", err)
	}
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
	return &salesv1.CreateSaleResponse{Sale: &salesv1.Sale{Id: "sale-1"}}, nil
}

func (salesServer) GetSale(_ context.Context, r *salesv1.GetSaleRequest) (*salesv1.GetSaleResponse, error) {
	states := map[string]salesv1.SaleStatus{
		"paid": salesv1.SaleStatus_SALE_STATUS_PAID, "voided": salesv1.SaleStatus_SALE_STATUS_VOIDED,
		"charged": salesv1.SaleStatus_SALE_STATUS_CHARGED, "open": salesv1.SaleStatus_SALE_STATUS_OPEN,
	}
	if st, ok := states[r.GetSaleId()]; ok {
		return &salesv1.GetSaleResponse{Sale: &salesv1.Sale{Id: r.GetSaleId(), Status: st}}, nil
	}
	if r.GetSaleId() == "bozuk" {
		return nil, status.Error(codes.Internal, "bozuk")
	}
	return nil, status.Error(codes.NotFound, "yok")
}

func TestSales(t *testing.T) {
	srv := &salesServer{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { salesv1.RegisterSalesServiceServer(s, srv) })
	sales := grpcclients.NewSales(conn)
	ctx := context.Background()

	lines := []app.SaleLine{{ProductID: "kofte", Name: "Köfte", Qty: 2, UnitPrice: 25000, TaxRate: 1000}}
	id, err := sales.CreateSale(ctx, "ali", lines)
	if err != nil || id != "sale-1" {
		t.Fatalf("CreateSale: %q %v", id, err)
	}
	got := srv.got.GetLines()
	if len(got) != 1 || got[0].GetQtyMilli() != 2000 || got[0].GetUnitPriceKurus() != 25000 || got[0].GetTaxRateBp() != 1000 {
		t.Errorf("line sent (the count must be converted to milli units): %v", srv.got)
	}
	if _, err := sales.CreateSale(ctx, "kapali", lines); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("server error code: %v", err)
	}

	for id, want := range map[string]app.SaleStatus{
		"paid": app.SalePaid, "voided": app.SaleVoided, "charged": app.SaleCharged, "open": app.SaleOpen,
		"yok": app.SaleVoided, // if there is no sale record it counts as cancelled
	} {
		if got, err := sales.Status(ctx, id); err != nil || got != want {
			t.Errorf("Status(%s) = %q %v, want %q", id, got, err, want)
		}
	}
	if _, err := sales.Status(ctx, "bozuk"); err == nil {
		t.Error("the server error was not returned")
	}
}

type deviceServer struct {
	devicev1.UnimplementedDeviceServiceServer
	printed *devicev1.PrintRequest
	failing bool
}

func (d deviceServer) GetStatus(context.Context, *devicev1.GetStatusRequest) (*devicev1.GetStatusResponse, error) {
	if d.failing {
		return nil, status.Error(codes.Internal, "yazıcı bozuk")
	}
	return &devicev1.GetStatusResponse{Columns: 42}, nil
}

func (d *deviceServer) Print(_ context.Context, r *devicev1.PrintRequest) (*devicev1.PrintResponse, error) {
	if d.failing {
		return nil, status.Error(codes.FailedPrecondition, "kağıt bitti")
	}
	d.printed = r
	return &devicev1.PrintResponse{}, nil
}

func TestPrinter(t *testing.T) {
	dev := &deviceServer{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { devicev1.RegisterDeviceServiceServer(s, dev) })
	p := grpcclients.NewPrinter(conn)
	ctx := context.Background()

	if cols, err := p.Columns(ctx); err != nil || cols != 42 {
		t.Errorf("Columns: %d %v", cols, err)
	}
	if err := p.Print(ctx, "mutfak", "Masa 4", []string{"2 x Köfte"}); err != nil {
		t.Fatal(err)
	}
	if dev.printed.GetStation() != "mutfak" || dev.printed.GetTitle() != "Masa 4" || len(dev.printed.GetLines()) != 1 {
		t.Errorf("printed: %v", dev.printed)
	}

	dev.failing = true
	// The printer's own message (e.g. "paper out") stays as it is.
	if err := p.Print(ctx, "", "x", nil); status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "kağıt bitti" {
		t.Errorf("Print error: %v", err)
	}
	if _, err := p.Columns(ctx); err == nil {
		t.Error("the Columns error was not returned")
	}
}

type staffServer struct {
	staffv1.UnimplementedStaffServiceServer
	action staffv1.Action
}

func (s *staffServer) Authorize(_ context.Context, r *staffv1.AuthorizeRequest) (*staffv1.AuthorizeResponse, error) {
	s.action = r.GetAction()
	switch r.GetCashierId() {
	case "garson":
		return nil, status.Error(codes.PermissionDenied, "müdür yetkisi gerekli")
	case "oturumsuz":
		return nil, status.Error(codes.Unauthenticated, "oturum yok")
	case "bozuk":
		return nil, status.Error(codes.Internal, "bozuk")
	}
	return &staffv1.AuthorizeResponse{ManagerId: "mudur-" + r.GetCashierId()}, nil
}

func TestStaff(t *testing.T) {
	srv := &staffServer{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { staffv1.RegisterStaffServiceServer(s, srv) })
	st := grpcclients.NewStaff(conn)
	ctx := context.Background()

	if id, err := st.VoidLine(ctx, "ali", ""); err != nil || id != "mudur-ali" || srv.action != staffv1.Action_ACTION_VOID_ORDER_LINE {
		t.Errorf("VoidLine: %q %v (action %v)", id, err, srv.action)
	}
	if id, err := st.EditFloor(ctx, "ali", "tok"); err != nil || id != "mudur-ali" || srv.action != staffv1.Action_ACTION_EDIT_FLOOR {
		t.Errorf("EditFloor: %q %v (action %v)", id, err, srv.action)
	}
	for _, who := range []string{"garson", "oturumsuz"} {
		if _, err := st.VoidLine(ctx, who, ""); !errors.Is(err, app.ErrNotAuthorized) {
			t.Errorf("%s: %v", who, err)
		}
	}
	if _, err := st.EditFloor(ctx, "bozuk", ""); err == nil || errors.Is(err, app.ErrNotAuthorized) {
		t.Errorf("a server error was taken as unauthorized: %v", err)
	}
}
