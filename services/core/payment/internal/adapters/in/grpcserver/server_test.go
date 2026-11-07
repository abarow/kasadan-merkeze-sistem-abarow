// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "abarow.com/gen/com/abarow/payment/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/pkg/money"
	"abarow.com/services/core/payment/internal/adapters/in/grpcserver"
	"abarow.com/services/core/payment/internal/adapters/out/sqlite"
	"abarow.com/services/core/payment/internal/adapters/out/terminalsim"
	"abarow.com/services/core/payment/internal/app"
)

func start(t *testing.T, limit money.Money) paymentv1.PaymentServiceClient {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "payment.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	term := &terminalsim.Terminal{Limit: limit, Log: slog.New(slog.DiscardHandler)}
	svc := app.NewService(st, term, time.Now)
	conn := grpctest.Conn(t, func(s *grpc.Server) { paymentv1.RegisterPaymentServiceServer(s, grpcserver.New(svc)) })
	return paymentv1.NewPaymentServiceClient(conn)
}

func pay(m paymentv1.PaymentMethod, amount int64) *paymentv1.Payment {
	return &paymentv1.Payment{Method: m, AmountKurus: amount}
}

func TestChargeAndRefund(t *testing.T) {
	c := start(t, 0)
	ctx := context.Background()

	r, err := c.Charge(ctx, &paymentv1.ChargeRequest{SaleId: "s1", Payments: []*paymentv1.Payment{
		pay(paymentv1.PaymentMethod_PAYMENT_METHOD_CASH, 1000),
		pay(paymentv1.PaymentMethod_PAYMENT_METHOD_CARD, 2500),
		pay(paymentv1.PaymentMethod_PAYMENT_METHOD_MEAL_CARD, 500),
	}})
	if err != nil || len(r.GetPayments()) != 3 {
		t.Fatalf("Charge: %v %v", r, err)
	}
	if r.GetPayments()[0].GetRef() != "" || r.GetPayments()[1].GetRef() == "" || r.GetPayments()[2].GetRef() == "" {
		t.Errorf("POS references (empty for cash, filled for cards): %v", r.GetPayments())
	}
	if r.GetPayments()[1].GetMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_CARD || r.GetPayments()[1].GetAmountKurus() != 2500 {
		t.Errorf("card payment: %v", r.GetPayments()[1])
	}

	// If requested again for the same sale the same payment returns (no double charge).
	again, err := c.Charge(ctx, &paymentv1.ChargeRequest{SaleId: "s1", Payments: []*paymentv1.Payment{pay(paymentv1.PaymentMethod_PAYMENT_METHOD_CARD, 2500)}})
	if err != nil || len(again.GetPayments()) != 3 {
		t.Errorf("tekrarlanan Charge: %v %v", again, err)
	}

	rf, err := c.Refund(ctx, &paymentv1.RefundRequest{SaleId: "s1"})
	if err != nil || len(rf.GetPayments()) != 3 {
		t.Fatalf("Refund: %v %v", rf, err)
	}
	for _, p := range rf.GetPayments() {
		if !p.GetRefunded() {
			t.Errorf("iade edilmedi: %v", p)
		}
	}
	if _, err := c.Refund(ctx, &paymentv1.RefundRequest{SaleId: "yok"}); status.Code(err) != codes.NotFound {
		t.Errorf("refund of a non-existent sale: %v", err)
	}
}

func TestChargeErrors(t *testing.T) {
	c := start(t, 5000)
	ctx := context.Background()

	if _, err := c.Charge(ctx, &paymentv1.ChargeRequest{SaleId: "s1"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("request with no payment: %v", err)
	}
	if _, err := c.Charge(ctx, &paymentv1.ChargeRequest{Payments: []*paymentv1.Payment{pay(paymentv1.PaymentMethod_PAYMENT_METHOD_CASH, 1)}}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("request with no sale id: %v", err)
	}
	if _, err := c.Charge(ctx, &paymentv1.ChargeRequest{SaleId: "s1", Payments: []*paymentv1.Payment{pay(paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED, 100)}}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("unknown method: %v", err)
	}
	// A card transaction above the POS limit is rejected.
	if _, err := c.Charge(ctx, &paymentv1.ChargeRequest{SaleId: "s2", Payments: []*paymentv1.Payment{pay(paymentv1.PaymentMethod_PAYMENT_METHOD_CARD, 9000)}}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("reddedilen kart: %v", err)
	}
}
