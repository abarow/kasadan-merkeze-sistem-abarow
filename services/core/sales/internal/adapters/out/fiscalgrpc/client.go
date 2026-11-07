// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package fiscalgrpc is the app.FiscalDevice adapter that connects with gRPC to the
// active fiscal adapter (simulator or ÖKC).
package fiscalgrpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/paymethod"
	"abarow.com/pkg/service"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// Client implements app.FiscalDevice.
type Client struct {
	c       fiscalv1.FiscalServiceClient
	Timeout time.Duration
	Now     func() time.Time
}

var _ app.FiscalDevice = (*Client)(nil)

// New returns a client over the given connection.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{c: fiscalv1.NewFiscalServiceClient(conn), Timeout: 30 * time.Second, Now: time.Now}
}

func (c *Client) Register(ctx context.Context, s *domain.Sale) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	resp, err := c.c.Register(ctx, &fiscalv1.RegisterRequest{Receipt: c.receipt(s)})
	if err != nil {
		return "", classify(service.RemoteErrorFrom("ÖKC", err))
	}
	return resp.GetFiscalNo(), nil
}

// classify marks the errors that say the device definitely did not perform the operation with
// app.ErrNotProcessed: FAILED_PRECONDITION (the payment could not be taken, the
// device is not ready), INVALID_ARGUMENT (the receipt is invalid), ABORTED (the receipt was
// cancelled). Connection errors and requests left without a result (UNAVAILABLE,
// DEADLINE_EXCEEDED) are uncertain, they are not marked.
func classify(err error) error {
	switch status.Code(err) {
	case codes.FailedPrecondition, codes.InvalidArgument, codes.Aborted:
		return fmt.Errorf("%w: %w", app.ErrNotProcessed, err)
	}
	return err
}

func (c *Client) receipt(s *domain.Sale) *fiscalv1.Receipt {
	r := &fiscalv1.Receipt{
		SaleId:      string(s.ID()),
		RegisterId:  s.RegisterID(),
		TotalKurus:  int64(s.Total()),
		ChangeKurus: int64(s.Change()),
		IssuedAt:    timestamppb.New(c.Now()),
	}
	for _, l := range s.Lines() {
		r.Lines = append(r.Lines, &fiscalv1.ReceiptLine{
			Name:           l.Name,
			QtyMilli:       int64(l.Qty),
			UnitPriceKurus: int64(l.UnitPrice),
			TotalKurus:     int64(l.Total()),
			TaxRateBp:      int64(l.TaxRate),
		})
	}
	for _, t := range s.TaxBreakdown() {
		r.Taxes = append(r.Taxes, &fiscalv1.TaxLine{RateBp: int64(t.Rate), GrossKurus: int64(t.Gross), TaxKurus: int64(t.Tax)})
	}
	for _, p := range s.Payments() {
		method := fiscalv1.PaymentMethod(paymethod.Number(string(p.Method)))
		r.Payments = append(r.Payments, &fiscalv1.Payment{Method: method, AmountKurus: int64(p.Amount), Collect: p.Collect})
	}
	return r
}
