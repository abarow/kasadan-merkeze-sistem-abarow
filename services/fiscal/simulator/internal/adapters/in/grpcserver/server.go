// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the fiscal printer simulator;
// it implements the fiscal.v1 contract.
package grpcserver

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
	"abarow.com/services/fiscal/simulator/internal/app"
	"abarow.com/services/fiscal/simulator/internal/domain"
)

// Server, FiscalService'i uygular.
type Server struct {
	fiscalv1.UnimplementedFiscalServiceServer
	svc *app.Service
}

// New binds the service to gRPC.
func New(svc *app.Service) *Server { return &Server{svc: svc} }

func (s *Server) Register(ctx context.Context, req *fiscalv1.RegisterRequest) (*fiscalv1.RegisterResponse, error) {
	no, err := s.svc.Register(ctx, fromProto(req.GetReceipt()))
	if err != nil {
		code := codes.Unavailable // a printer or counter error; can be retried
		if errors.Is(err, domain.ErrInvalidReceipt) {
			code = codes.InvalidArgument
		}
		return nil, status.Error(code, err.Error())
	}
	return &fiscalv1.RegisterResponse{FiscalNo: no}, nil
}

func fromProto(r *fiscalv1.Receipt) domain.Receipt {
	out := domain.Receipt{
		SaleID:     r.GetSaleId(),
		RegisterID: r.GetRegisterId(),
		Total:      money.Money(r.GetTotalKurus()),
		Change:     money.Money(r.GetChangeKurus()),
		At:         time.Now(),
	}
	if r.GetIssuedAt() != nil {
		out.At = r.GetIssuedAt().AsTime()
	}
	for _, l := range r.GetLines() {
		out.Lines = append(out.Lines, domain.Line{
			Name:      l.GetName(),
			Qty:       money.Qty(l.GetQtyMilli()),
			UnitPrice: money.Money(l.GetUnitPriceKurus()),
			Total:     money.Money(l.GetTotalKurus()),
			TaxRate:   money.Rate(l.GetTaxRateBp()),
		})
	}
	for _, t := range r.GetTaxes() {
		out.Taxes = append(out.Taxes, domain.TaxLine{Rate: money.Rate(t.GetRateBp()), Gross: money.Money(t.GetGrossKurus()), Tax: money.Money(t.GetTaxKurus())})
	}
	for _, p := range r.GetPayments() {
		method := paymethod.Name(int32(p.GetMethod()))
		out.Payments = append(out.Payments, domain.Payment{Method: method, Amount: money.Money(p.GetAmountKurus())})
	}
	return out
}
