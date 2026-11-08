// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package fiscalgrpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/core/sales/internal/adapters/out/fiscalgrpc"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

type server struct {
	fiscalv1.UnimplementedFiscalServiceServer
	got *fiscalv1.RegisterRequest
	err error
}

func (s *server) Register(_ context.Context, r *fiscalv1.RegisterRequest) (*fiscalv1.RegisterResponse, error) {
	s.got = r
	if s.err != nil {
		return nil, s.err
	}
	return &fiscalv1.RegisterResponse{FiscalNo: "F-0001"}, nil
}

func sale(t *testing.T) *domain.Sale {
	t.Helper()
	s, err := domain.NewSale("s1", "register-1", "ayse", []domain.Line{
		{ProductID: "ekmek", Barcode: "1", Name: "Ekmek", Qty: 2000, UnitPrice: 1500, TaxRate: 100},
		{ProductID: "sabun", Barcode: "2", Name: "Sabun", Qty: 1000, UnitPrice: 1200, TaxRate: 2000},
	}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCharge([]domain.Payment{{Method: domain.PaymentCash, Amount: 5000, Collect: true}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRegisterBuildsReceipt(t *testing.T) {
	srv := &server{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { fiscalv1.RegisterFiscalServiceServer(s, srv) })
	c := fiscalgrpc.New(conn)
	issued := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return issued }

	s := sale(t)
	no, err := c.Register(context.Background(), s)
	if err != nil || no != "F-0001" {
		t.Fatalf("Register: %q %v", no, err)
	}
	r := srv.got.GetReceipt()
	if r.GetSaleId() != "s1" || r.GetRegisterId() != "register-1" || r.GetTotalKurus() != int64(s.Total()) || r.GetChangeKurus() != int64(s.Change()) {
		t.Errorf("receipt header: %v", r)
	}
	if !r.GetIssuedAt().AsTime().Equal(issued) {
		t.Errorf("issue time %v", r.GetIssuedAt().AsTime())
	}
	if len(r.GetLines()) != 2 || r.GetLines()[0].GetQtyMilli() != 2000 || r.GetLines()[0].GetTotalKurus() != 3000 {
		t.Errorf("receipt lines: %v", r.GetLines())
	}
	if len(r.GetTaxes()) != 2 {
		t.Errorf("VAT breakdown: %v", r.GetTaxes())
	}
	if len(r.GetPayments()) != 1 || r.GetPayments()[0].GetMethod() != fiscalv1.PaymentMethod_PAYMENT_METHOD_CASH ||
		r.GetPayments()[0].GetAmountKurus() != 5000 || !r.GetPayments()[0].GetCollect() {
		t.Errorf("payments: %v", r.GetPayments())
	}
}

func TestRegisterErrors(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		notProcessed bool
	}{
		{"cihaz reddetti", status.Error(codes.FailedPrecondition, "kağıt bitti"), true},
		{"geçersiz fiş", status.Error(codes.InvalidArgument, "x"), true},
		{"iptal edildi", status.Error(codes.Aborted, "x"), true},
		{"cihaz hatası", status.Error(codes.Internal, "cihaz yanıtı belirsiz"), false},
	}
	for _, tc := range tests {
		srv := &server{err: tc.err}
		conn := grpctest.Conn(t, func(s *grpc.Server) { fiscalv1.RegisterFiscalServiceServer(s, srv) })
		_, err := fiscalgrpc.New(conn).Register(context.Background(), sale(t))
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if got := errors.Is(err, app.ErrNotProcessed); got != tc.notProcessed {
			t.Errorf("%s: ErrNotProcessed = %v, want %v (%v)", tc.name, got, tc.notProcessed, err)
		}
		// The device's own message (e.g. "paper out") is kept all the way to the cashier.
		if want := status.Convert(tc.err).Message(); !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the device message %q was lost: %v", tc.name, want, err)
		}
	}
}
