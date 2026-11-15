// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/fiscal/simulator/internal/adapters/in/grpcserver"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/memory"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/writer"
	"abarow.com/services/fiscal/simulator/internal/app"
	"abarow.com/services/fiscal/simulator/internal/domain"
)

type drawer struct{ reasons []string }

func (d *drawer) Open(_ context.Context, reason string) { d.reasons = append(d.reasons, reason) }

type brokenPrinter struct{}

func (brokenPrinter) Print(context.Context, string, string) error { return errors.New("kağıt bitti") }

func start(t *testing.T, p app.Printer) (fiscalv1.FiscalServiceClient, *drawer) {
	t.Helper()
	d := &drawer{}
	svc := app.NewService(&memory.Counter{}, p, d, domain.Layout{Width: 48, Header: []string{"ÖRNEK MARKET"}})
	conn := grpctest.Conn(t, func(s *grpc.Server) { fiscalv1.RegisterFiscalServiceServer(s, grpcserver.New(svc)) })
	return fiscalv1.NewFiscalServiceClient(conn), d
}

func receipt(cash bool) *fiscalv1.RegisterRequest {
	method := fiscalv1.PaymentMethod_PAYMENT_METHOD_CARD
	if cash {
		method = fiscalv1.PaymentMethod_PAYMENT_METHOD_CASH
	}
	return &fiscalv1.RegisterRequest{Receipt: &fiscalv1.Receipt{
		SaleId: "s1", RegisterId: "register-1", TotalKurus: 3000, ChangeKurus: 0,
		IssuedAt: timestamppb.New(time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC)),
		Lines:    []*fiscalv1.ReceiptLine{{Name: "Ekmek", QtyMilli: 2000, UnitPriceKurus: 1500, TotalKurus: 3000, TaxRateBp: 100}},
		Taxes:    []*fiscalv1.TaxLine{{RateBp: 100, GrossKurus: 3000, TaxKurus: 30}},
		Payments: []*fiscalv1.Payment{{Method: method, AmountKurus: 3000}},
	}}
}

func TestRegisterPrintsAndOpensDrawerForCash(t *testing.T) {
	var out bytes.Buffer
	c, d := start(t, writer.New(&out))
	ctx := context.Background()

	r, err := c.Register(ctx, receipt(true))
	if err != nil || r.GetFiscalNo() != "SIM-000001" {
		t.Fatalf("Register: %v %v", r, err)
	}
	if !strings.Contains(out.String(), "Ekmek") || !strings.Contains(out.String(), domain.Warning) || !strings.Contains(out.String(), "SIM-000001") {
		t.Errorf("receipt output:\n%s", out.String())
	}
	if len(d.reasons) != 1 || !strings.Contains(d.reasons[0], "SIM-000001") {
		t.Errorf("drawer on a cash sale: %v", d.reasons)
	}

	// In a card sale the drawer is not opened and the number increases.
	r, err = c.Register(ctx, receipt(false))
	if err != nil || r.GetFiscalNo() != "SIM-000002" || len(d.reasons) != 1 {
		t.Errorf("card sale: %v %v (drawer %v)", r, err, d.reasons)
	}
}

func TestRegisterErrors(t *testing.T) {
	var out bytes.Buffer
	c, _ := start(t, writer.New(&out))
	bad := receipt(true)
	bad.Receipt.TotalKurus = 1 // does not add up with the lines
	if _, err := c.Register(context.Background(), bad); status.Code(err) != codes.InvalidArgument {
		t.Errorf("inconsistent receipt: %v", err)
	}
	if out.Len() != 0 {
		t.Error("an invalid receipt was printed")
	}

	broken, _ := start(t, brokenPrinter{})
	// A printer error counts as retryable (Unavailable).
	if _, err := broken.Register(context.Background(), receipt(true)); status.Code(err) != codes.Unavailable {
		t.Errorf("printer error: %v", err)
	}
}
