// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package paymentgrpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "abarow.com/gen/com/abarow/payment/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/core/sales/internal/adapters/out/paymentgrpc"
	"abarow.com/services/core/sales/internal/domain"
)

type server struct {
	paymentv1.UnimplementedPaymentServiceServer
	charged  *paymentv1.ChargeRequest
	refunded string
}

func (s *server) Charge(_ context.Context, r *paymentv1.ChargeRequest) (*paymentv1.ChargeResponse, error) {
	s.charged = r
	if r.GetSaleId() == "reddedildi" {
		return nil, status.Error(codes.FailedPrecondition, "kart reddedildi")
	}
	out := &paymentv1.ChargeResponse{}
	for _, p := range r.GetPayments() {
		ref := p.GetRef()
		if p.GetMethod() == paymentv1.PaymentMethod_PAYMENT_METHOD_CARD {
			ref = "pos-123"
		}
		out.Payments = append(out.Payments, &paymentv1.Payment{Method: p.GetMethod(), AmountKurus: p.GetAmountKurus(), Ref: ref})
	}
	return out, nil
}

func (s *server) Refund(_ context.Context, r *paymentv1.RefundRequest) (*paymentv1.RefundResponse, error) {
	s.refunded = r.GetSaleId()
	if r.GetSaleId() == "geri-verilemedi" {
		return nil, status.Error(codes.Internal, "POS yanıt vermedi")
	}
	return &paymentv1.RefundResponse{}, nil
}

func TestChargeAndRefund(t *testing.T) {
	srv := &server{}
	conn := grpctest.Conn(t, func(s *grpc.Server) { paymentv1.RegisterPaymentServiceServer(s, srv) })
	c := paymentgrpc.New(conn)
	ctx := context.Background()

	got, err := c.Charge(ctx, "s1", []domain.Payment{
		{Method: domain.PaymentCash, Amount: 1000},
		{Method: domain.PaymentCard, Amount: 500},
		{Method: domain.PaymentMealCard, Amount: 250, Ref: "r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.charged.GetSaleId() != "s1" || len(srv.charged.GetPayments()) != 3 {
		t.Fatalf("sunucuya giden: %v", srv.charged)
	}
	want := []domain.Payment{
		{Method: domain.PaymentCash, Amount: 1000},
		{Method: domain.PaymentCard, Amount: 500, Ref: "pos-123"},
		{Method: domain.PaymentMealCard, Amount: 250, Ref: "r1"},
	}
	for i, w := range want {
		if got[i].Method != w.Method || got[i].Amount != w.Amount || got[i].Ref != w.Ref {
			t.Errorf("ödeme %d: %+v, istenen %+v", i, got[i], w)
		}
	}
	if _, err := c.Charge(ctx, "reddedildi", []domain.Payment{{Method: domain.PaymentCard, Amount: 1}}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("reddedilen ödeme: %v", err)
	}

	if err := c.Refund(ctx, "s1"); err != nil || srv.refunded != "s1" {
		t.Errorf("Refund: %v (%q)", err, srv.refunded)
	}
	if err := c.Refund(ctx, "geri-verilemedi"); err == nil {
		t.Error("iade hatası döndürülmedi")
	}
}

func TestTimeout(t *testing.T) {
	conn := grpctest.Conn(t, func(s *grpc.Server) { paymentv1.RegisterPaymentServiceServer(s, slow{}) })
	c := paymentgrpc.New(conn)
	c.Timeout = 20 * time.Millisecond
	_, err := c.Charge(context.Background(), "s1", []domain.Payment{{Method: domain.PaymentCash, Amount: 1}})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("zaman aşımı: %v", err)
	}
}

type slow struct {
	paymentv1.UnimplementedPaymentServiceServer
}

func (slow) Charge(ctx context.Context, _ *paymentv1.ChargeRequest) (*paymentv1.ChargeResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
