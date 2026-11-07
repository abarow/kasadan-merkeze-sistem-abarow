// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the Beko fiscal printer adapter;
// it implements the fiscal.v1 contract (the same contract as the fiscal printer simulator).
package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
	"abarow.com/services/fiscal/beko/internal/app"
	"abarow.com/services/fiscal/beko/internal/domain"
)

// Server, FiscalService'i uygular.
type Server struct {
	fiscalv1.UnimplementedFiscalServiceServer
	svc *app.Service
}

// New binds the service to gRPC.
func New(svc *app.Service) *Server { return &Server{svc: svc} }

// Register has the receipt recorded on the fiscal printer. The error codes are
// chosen according to sales' decision: FailedPrecondition means the device
// definitely did not do the operation (not ready, did not accept the basket, card declined);
// if sales already charged the card it leaves the sale open. Aborted means the receipt was
// cancelled on the fiscal printer; InvalidArgument means the receipt itself is not suitable for
// the fiscal printer. Unavailable means the result is uncertain (the receipt may have been issued,
// the card may have been charged).
func (s *Server) Register(ctx context.Context, req *fiscalv1.RegisterRequest) (*fiscalv1.RegisterResponse, error) {
	no, err := s.svc.Register(ctx, fromProto(req.GetReceipt()))
	if err != nil {
		return nil, status.Error(code(err), err.Error())
	}
	return &fiscalv1.RegisterResponse{FiscalNo: no}, nil
}

func code(err error) codes.Code {
	switch {
	case errors.Is(err, domain.ErrInvalidReceipt), errors.Is(err, domain.ErrUnmappedTax), errors.Is(err, domain.ErrCardNotCollected):
		return codes.InvalidArgument
	case errors.Is(err, app.ErrDeclined), errors.Is(err, app.ErrNotReady), errors.Is(err, app.ErrRejected):
		return codes.FailedPrecondition
	case errors.Is(err, app.ErrVoided):
		return codes.Aborted
	}
	return codes.Unavailable
}

func fromProto(r *fiscalv1.Receipt) domain.Receipt {
	out := domain.Receipt{SaleID: r.GetSaleId(), Total: money.Money(r.GetTotalKurus()), Change: money.Money(r.GetChangeKurus())}
	for _, l := range r.GetLines() {
		out.Lines = append(out.Lines, domain.Line{
			Name: l.GetName(), Qty: money.Qty(l.GetQtyMilli()), UnitPrice: money.Money(l.GetUnitPriceKurus()),
			Total: money.Money(l.GetTotalKurus()), TaxRate: money.Rate(l.GetTaxRateBp()),
		})
	}
	for _, p := range r.GetPayments() {
		out.Payments = append(out.Payments, domain.Payment{
			Method: paymethod.Name(int32(p.GetMethod())), Amount: money.Money(p.GetAmountKurus()), Collect: p.GetCollect(),
		})
	}
	return out
}
