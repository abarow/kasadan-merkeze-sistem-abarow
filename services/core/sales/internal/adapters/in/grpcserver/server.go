// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the sales service.
package grpcserver

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// Server, SalesService'i uygular.
type Server struct {
	salesv1.UnimplementedSalesServiceServer
	svc   *app.Service
	relay *app.Relay
}

// New connects the service and the outbox relay to gRPC.
func New(svc *app.Service, relay *app.Relay) *Server { return &Server{svc: svc, relay: relay} }

func (s *Server) GetOutboxStatus(ctx context.Context, _ *salesv1.GetOutboxStatusRequest) (*salesv1.GetOutboxStatusResponse, error) {
	n, err := s.relay.Backlog(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &salesv1.GetOutboxStatusResponse{Pending: n}, nil
}

func (s *Server) CreateSale(ctx context.Context, req *salesv1.CreateSaleRequest) (*salesv1.CreateSaleResponse, error) {
	lines := make([]domain.Line, 0, len(req.GetLines()))
	for _, l := range req.GetLines() {
		lines = append(lines, domain.Line{
			ProductID: l.GetProductId(),
			Barcode:   l.GetBarcode(),
			Name:      l.GetName(),
			Qty:       money.Qty(l.GetQtyMilli()),
			UnitPrice: money.Money(l.GetUnitPriceKurus()),
			TaxRate:   money.Rate(l.GetTaxRateBp()),
		})
	}
	sale, err := s.svc.CreateSale(ctx, req.GetCashierId(), lines)
	if err != nil {
		return nil, toStatus(err)
	}
	return &salesv1.CreateSaleResponse{Sale: toProto(sale)}, nil
}

func (s *Server) GetSale(ctx context.Context, req *salesv1.GetSaleRequest) (*salesv1.GetSaleResponse, error) {
	sale, err := s.svc.GetSale(ctx, domain.SaleID(req.GetSaleId()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &salesv1.GetSaleResponse{Sale: toProto(sale)}, nil
}

func (s *Server) ListSales(ctx context.Context, req *salesv1.ListSalesRequest) (*salesv1.ListSalesResponse, error) {
	sales, err := s.svc.ListSales(ctx, statusFromProto(req.GetStatus()), int(req.GetLimit()))
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &salesv1.ListSalesResponse{Sales: make([]*salesv1.Sale, 0, len(sales))}
	for _, sale := range sales {
		resp.Sales = append(resp.Sales, toProto(sale))
	}
	return resp, nil
}

func (s *Server) Checkout(ctx context.Context, req *salesv1.CheckoutRequest) (*salesv1.CheckoutResponse, error) {
	payments := make([]domain.Payment, 0, len(req.GetPayments()))
	for _, p := range req.GetPayments() {
		payments = append(payments, domain.Payment{Method: methodFromProto(p.GetMethod()), Amount: money.Money(p.GetAmountKurus()), Ref: p.GetRef()})
	}
	sale, err := s.svc.Checkout(ctx, domain.SaleID(req.GetSaleId()), payments)
	if err != nil {
		return nil, toStatus(err)
	}
	return &salesv1.CheckoutResponse{Sale: toProto(sale)}, nil
}

func (s *Server) VoidSale(ctx context.Context, req *salesv1.VoidSaleRequest) (*salesv1.VoidSaleResponse, error) {
	sale, err := s.svc.VoidSale(ctx, domain.SaleID(req.GetSaleId()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &salesv1.VoidSaleResponse{Sale: toProto(sale)}, nil
}

func (s *Server) RefundCharged(ctx context.Context, req *salesv1.RefundChargedRequest) (*salesv1.RefundChargedResponse, error) {
	sale, err := s.svc.RefundCharged(ctx, domain.SaleID(req.GetSaleId()))
	if err != nil {
		return nil, toStatus(err)
	}
	return &salesv1.RefundChargedResponse{Sale: toProto(sale)}, nil
}

// toStatus turns errors into gRPC codes. gRPC errors coming from the payment or fiscal
// service are passed on with their own codes.
func toStatus(err error) error {
	code := codes.Internal
	switch {
	case errors.Is(err, app.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, domain.ErrNoLines), errors.Is(err, domain.ErrInvalidLine),
		errors.Is(err, domain.ErrInvalidPayment), errors.Is(err, domain.ErrInsufficientPayment),
		errors.Is(err, domain.ErrExcessNonCash), errors.Is(err, domain.ErrInvalidStatus),
		errors.Is(err, calc.ErrTooLarge):
		code = codes.InvalidArgument
	case errors.Is(err, domain.ErrNotOpen), errors.Is(err, domain.ErrNotCharged),
		errors.Is(err, app.ErrNotProcessed), errors.Is(err, app.ErrCollectedByDevice):
		code = codes.FailedPrecondition
	default:
		if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
			code = st.Code()
		}
	}
	return status.Error(code, err.Error())
}

func toProto(s *domain.Sale) *salesv1.Sale {
	out := &salesv1.Sale{
		Id:            string(s.ID()),
		RegisterId:    s.RegisterID(),
		CashierId:     s.CashierID(),
		Status:        statusToProto(s.Status()),
		TotalKurus:    int64(s.Total()),
		TaxTotalKurus: int64(s.TaxTotal()),
		ChangeKurus:   int64(s.Change()),
		ReceiptNo:     s.ReceiptNo(),
		FiscalNo:      s.FiscalNo(),
		CreatedAt:     timestamp(s.CreatedAt()),
		PaidAt:        timestamp(s.PaidAt()),
		VoidedAt:      timestamp(s.VoidedAt()),
		Refunded:      s.Refunded(),
	}
	for _, l := range s.Lines() {
		out.Lines = append(out.Lines, &salesv1.SaleLine{
			ProductId:      l.ProductID,
			Barcode:        l.Barcode,
			Name:           l.Name,
			QtyMilli:       int64(l.Qty),
			UnitPriceKurus: int64(l.UnitPrice),
			TaxRateBp:      int64(l.TaxRate),
		})
	}
	for _, p := range s.Payments() {
		out.Payments = append(out.Payments, &salesv1.Payment{Method: methodToProto(p.Method), AmountKurus: int64(p.Amount), Ref: p.Ref})
	}
	for _, t := range s.TaxBreakdown() {
		out.Taxes = append(out.Taxes, &salesv1.TaxLine{RateBp: int64(t.Rate), GrossKurus: int64(t.Gross), TaxKurus: int64(t.Tax)})
	}
	return out
}

func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func statusFromProto(s salesv1.SaleStatus) domain.Status {
	switch s {
	case salesv1.SaleStatus_SALE_STATUS_OPEN:
		return domain.StatusOpen
	case salesv1.SaleStatus_SALE_STATUS_CHARGED:
		return domain.StatusCharged
	case salesv1.SaleStatus_SALE_STATUS_PAID:
		return domain.StatusPaid
	case salesv1.SaleStatus_SALE_STATUS_VOIDED:
		return domain.StatusVoided
	}
	return ""
}

func statusToProto(s domain.Status) salesv1.SaleStatus {
	switch s {
	case domain.StatusOpen:
		return salesv1.SaleStatus_SALE_STATUS_OPEN
	case domain.StatusCharged:
		return salesv1.SaleStatus_SALE_STATUS_CHARGED
	case domain.StatusPaid:
		return salesv1.SaleStatus_SALE_STATUS_PAID
	case domain.StatusVoided:
		return salesv1.SaleStatus_SALE_STATUS_VOIDED
	}
	return salesv1.SaleStatus_SALE_STATUS_UNSPECIFIED
}

func methodFromProto(m salesv1.PaymentMethod) domain.PaymentMethod {
	return domain.PaymentMethod(paymethod.Name(int32(m)))
}

func methodToProto(m domain.PaymentMethod) salesv1.PaymentMethod {
	return salesv1.PaymentMethod(paymethod.Number(string(m)))
}
