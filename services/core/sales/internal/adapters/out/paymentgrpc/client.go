// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package paymentgrpc is the app.PaymentGateway adapter that connects to the payment
// service with gRPC.
package paymentgrpc

import (
	"context"
	"time"

	"google.golang.org/grpc"

	paymentv1 "abarow.com/gen/com/abarow/payment/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
	"abarow.com/pkg/service"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// Client, app.PaymentGateway'i uygular.
type Client struct {
	c paymentv1.PaymentServiceClient
	// Timeout is the time of a single payment request. It is long since for a card it
	// covers the customer entering their PIN.
	Timeout time.Duration
}

var _ app.PaymentGateway = (*Client)(nil)

// New returns a client over the given connection.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{c: paymentv1.NewPaymentServiceClient(conn), Timeout: 90 * time.Second}
}

func (c *Client) Charge(ctx context.Context, id domain.SaleID, payments []domain.Payment) ([]domain.Payment, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req := &paymentv1.ChargeRequest{SaleId: string(id)}
	for _, p := range payments {
		req.Payments = append(req.Payments, &paymentv1.Payment{Method: methodToProto(p.Method), AmountKurus: int64(p.Amount), Ref: p.Ref})
	}
	resp, err := c.c.Charge(ctx, req)
	if err != nil {
		return nil, service.RemoteErrorFrom("ödeme servisi", err)
	}
	out := make([]domain.Payment, 0, len(resp.GetPayments()))
	for _, p := range resp.GetPayments() {
		out = append(out, domain.Payment{Method: methodFromProto(p.GetMethod()), Amount: money.Money(p.GetAmountKurus()), Ref: p.GetRef()})
	}
	return out, nil
}

func (c *Client) Refund(ctx context.Context, id domain.SaleID) error {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	_, err := c.c.Refund(ctx, &paymentv1.RefundRequest{SaleId: string(id)})
	return service.RemoteErrorFrom("ödeme servisi", err)
}

func methodToProto(m domain.PaymentMethod) paymentv1.PaymentMethod {
	return paymentv1.PaymentMethod(paymethod.Number(string(m)))
}

func methodFromProto(m paymentv1.PaymentMethod) domain.PaymentMethod {
	return domain.PaymentMethod(paymethod.Name(int32(m)))
}
