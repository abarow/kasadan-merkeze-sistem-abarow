// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package grpcserver is the gRPC driving adapter of the payment service.
package grpcserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "abarow.com/gen/com/abarow/payment/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
)

// Server, PaymentService'i uygular.
type Server struct {
	paymentv1.UnimplementedPaymentServiceServer
	svc *app.Service
}

// New connects the service to gRPC.
func New(svc *app.Service) *Server { return &Server{svc: svc} }

func (s *Server) Charge(ctx context.Context, req *paymentv1.ChargeRequest) (*paymentv1.ChargeResponse, error) {
	payments := make([]domain.Payment, 0, len(req.GetPayments()))
	for _, p := range req.GetPayments() {
		payments = append(payments, domain.Payment{Method: methodFromProto(p.GetMethod()), Amount: money.Money(p.GetAmountKurus()), Ref: p.GetRef()})
	}
	charged, err := s.svc.Charge(ctx, req.GetSaleId(), payments)
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &paymentv1.ChargeResponse{}
	for _, p := range charged {
		resp.Payments = append(resp.Payments, &paymentv1.Payment{Method: methodToProto(p.Method), AmountKurus: int64(p.Amount), Ref: p.Ref})
	}
	return resp, nil
}

func (s *Server) Refund(ctx context.Context, req *paymentv1.RefundRequest) (*paymentv1.RefundResponse, error) {
	refunded, err := s.svc.Refund(ctx, req.GetSaleId())
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &paymentv1.RefundResponse{}
	for _, p := range refunded {
		resp.Payments = append(resp.Payments, &paymentv1.Payment{Method: methodToProto(p.Method), AmountKurus: int64(p.Amount), Ref: p.Ref, Refunded: p.Refunded})
	}
	return resp, nil
}

func toStatus(err error) error {
	code := codes.Unavailable
	switch {
	case errors.Is(err, app.ErrNotFound):
		code = codes.NotFound
	case errors.Is(err, domain.ErrInvalidRequest):
		code = codes.InvalidArgument
	case errors.Is(err, app.ErrDeclined):
		code = codes.FailedPrecondition
	}
	return status.Error(code, err.Error())
}

func methodFromProto(m paymentv1.PaymentMethod) domain.Method {
	return domain.Method(paymethod.Name(int32(m)))
}

func methodToProto(m domain.Method) paymentv1.PaymentMethod {
	return paymentv1.PaymentMethod(paymethod.Number(string(m)))
}
