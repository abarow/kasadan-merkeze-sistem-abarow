// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/sales/internal/adapters/in/grpcserver"
	"abarow.com/services/core/sales/internal/adapters/out/postgres"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/app/apptest"
)

func start(t *testing.T, fiscal *apptest.Fiscal) salesv1.SalesServiceClient {
	t.Helper()
	st, err := postgres.Open(context.Background(), pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := app.NewService(st, &apptest.Payments{}, fiscal, apptest.Config())
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	// The relay is not run: the events wait in the outbox.
	relay := app.NewRelay(st, nil, time.Second, slog.Default())
	salesv1.RegisterSalesServiceServer(srv, grpcserver.New(svc, relay))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return salesv1.NewSalesServiceClient(conn)
}

var testLines = []*salesv1.SaleLine{
	{ProductId: "p1", Barcode: "8690000000011", Name: "Deterjan 1 L", QtyMilli: 1000, UnitPriceKurus: 1299, TaxRateBp: 2000},
	{ProductId: "p2", Barcode: "2700001012500", Name: "Domates", QtyMilli: 1250, UnitPriceKurus: 8990, TaxRateBp: 100},
}

func card(amount int64) []*salesv1.Payment {
	return []*salesv1.Payment{{Method: salesv1.PaymentMethod_PAYMENT_METHOD_CARD, AmountKurus: amount}}
}

func TestCheckoutOverGRPC(t *testing.T) {
	c := start(t, &apptest.Fiscal{})
	ctx := context.Background()

	created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: testLines})
	if err != nil {
		t.Fatalf("CreateSale: %v", err)
	}
	if s := created.GetSale(); s.GetStatus() != salesv1.SaleStatus_SALE_STATUS_OPEN || s.GetTotalKurus() != 12537 || len(s.GetLines()) != 2 || s.GetCreatedAt() == nil {
		t.Fatalf("created sale is wrong: %v", s)
	}

	done, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId(), Payments: card(12537)})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	s := done.GetSale()
	if s.GetStatus() != salesv1.SaleStatus_SALE_STATUS_PAID || s.GetReceiptNo() != 1 || s.GetFiscalNo() != "SIM-000001" ||
		s.GetTaxTotalKurus() != 328 || len(s.GetTaxes()) != 2 || s.GetPayments()[0].GetRef() != "REF-1" || s.GetPaidAt() == nil {
		t.Errorf("closed sale is wrong: %v", s)
	}
	// sale.completed.v1 is waiting to be published.
	if out, err := c.GetOutboxStatus(ctx, &salesv1.GetOutboxStatusRequest{}); err != nil || out.GetPending() != 1 {
		t.Errorf("GetOutboxStatus = %v, %v; istenen 1 bekleyen", out, err)
	}
}

func TestErrorCodes(t *testing.T) {
	c := start(t, &apptest.Fiscal{})
	ctx := context.Background()
	created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: testLines})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetSale().GetId()

	tests := []struct {
		name string
		call func() error
		want codes.Code
	}{
		{"olmayan satış", func() error {
			_, err := c.GetSale(ctx, &salesv1.GetSaleRequest{SaleId: "yok"})
			return err
		}, codes.NotFound},
		{"satırsız satış", func() error {
			_, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{})
			return err
		}, codes.InvalidArgument},
		{"eksik ödeme", func() error {
			_, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: id, Payments: card(100)})
			return err
		}, codes.InvalidArgument},
		{"yöntemsiz ödeme", func() error {
			_, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: id, Payments: []*salesv1.Payment{{AmountKurus: 12537}}})
			return err
		}, codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call()); got != tt.want {
				t.Errorf("kod = %v, istenen %v", got, tt.want)
			}
		})
	}

	if _, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: id, Payments: card(12537)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VoidSale(ctx, &salesv1.VoidSaleRequest{SaleId: id}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("cancelling a closed sale: code = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestDownstreamErrorCodeIsPropagated(t *testing.T) {
	fiscal := &apptest.Fiscal{Err: status.Error(codes.Unavailable, "ÖKC yanıt vermiyor")}
	c := start(t, fiscal)
	ctx := context.Background()
	created, err := c.CreateSale(ctx, &salesv1.CreateSaleRequest{Lines: testLines})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId(), Payments: card(12537)})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("kod = %v, istenen Unavailable (%v)", status.Code(err), err)
	}
	got, err := c.GetSale(ctx, &salesv1.GetSaleRequest{SaleId: created.GetSale().GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetSale().GetStatus() != salesv1.SaleStatus_SALE_STATUS_CHARGED {
		t.Errorf("durum %v, istenen CHARGED", got.GetSale().GetStatus())
	}

	// A sale whose receipt could not be printed is listed at the register; when completed it leaves the list.
	charged, err := c.ListSales(ctx, &salesv1.ListSalesRequest{Status: salesv1.SaleStatus_SALE_STATUS_CHARGED})
	if err != nil || len(charged.GetSales()) != 1 || charged.GetSales()[0].GetId() != created.GetSale().GetId() {
		t.Fatalf("charged sales: %v, %v", charged, err)
	}
	fiscal.Err = nil
	if _, err := c.Checkout(ctx, &salesv1.CheckoutRequest{SaleId: created.GetSale().GetId()}); err != nil {
		t.Fatal(err)
	}
	if charged, _ := c.ListSales(ctx, &salesv1.ListSalesRequest{Status: salesv1.SaleStatus_SALE_STATUS_CHARGED}); len(charged.GetSales()) != 0 {
		t.Errorf("the completed sale is still in the list: %v", charged)
	}
	if _, err := c.ListSales(ctx, &salesv1.ListSalesRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("durumsuz liste: %v", err)
	}
}
