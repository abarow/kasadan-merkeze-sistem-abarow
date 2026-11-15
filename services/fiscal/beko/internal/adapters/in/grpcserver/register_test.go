// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/pkg/money"
	"abarow.com/services/fiscal/beko/internal/adapters/in/grpcserver"
	"abarow.com/services/fiscal/beko/internal/app"
	"abarow.com/services/fiscal/beko/internal/domain"
)

// bridge stands in for the Token bridge: it accepts the basket (or refuses it)
// and reports the result as an event.
type bridge struct {
	events chan app.Event
	mu     sync.Mutex
	accept string
}

func (b *bridge) Events() <-chan app.Event { return b.events }

func (b *bridge) Call(_ context.Context, op string, payload []byte) ([]byte, error) {
	switch op {
	case "fiscalinfo":
		return []byte(`{"sectionCount":2}`), nil
	case "basket":
		b.mu.Lock()
		accept := b.accept
		b.mu.Unlock()
		if accept == "1" {
			s := string(payload)
			i := strings.Index(s, `"basketID":"`) + len(`"basketID":"`)
			id := s[i : i+strings.Index(s[i:], `"`)]
			b.events <- app.Event{Kind: "serial", Ref: "3", Payload: []byte(fmt.Sprintf(
				`{"basketID":%q,"message":"OK","receiptNo":2,"status":0,"zNo":12}`, id))}
		}
		return []byte(accept), nil
	}
	return nil, errors.New("bilinmeyen işlem")
}

func start(t *testing.T, ready bool) (fiscalv1.FiscalServiceClient, *bridge) {
	t.Helper()
	b := &bridge{events: make(chan app.Event, 16), accept: "1"}
	cfg := domain.Config{Sections: map[money.Rate]int{1000: 1}}
	svc := app.NewService(b, cfg, 300*time.Millisecond, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	if ready {
		b.events <- app.Event{Kind: "ready"}
		for i := 0; svc.Ready() != nil; i++ {
			if i > 200 {
				t.Fatal("ÖKC did not become ready")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	conn := grpctest.Conn(t, func(s *grpc.Server) { fiscalv1.RegisterFiscalServiceServer(s, grpcserver.New(svc)) })
	return fiscalv1.NewFiscalServiceClient(conn), b
}

func receipt(id string) *fiscalv1.RegisterRequest {
	return &fiscalv1.RegisterRequest{Receipt: &fiscalv1.Receipt{
		SaleId: id, TotalKurus: 1200,
		Lines:    []*fiscalv1.ReceiptLine{{Name: "Ekmek", QtyMilli: 1000, UnitPriceKurus: 1200, TotalKurus: 1200, TaxRateBp: 1000}},
		Payments: []*fiscalv1.Payment{{Method: fiscalv1.PaymentMethod_PAYMENT_METHOD_CASH, AmountKurus: 1200}},
	}}
}

func TestRegister(t *testing.T) {
	c, b := start(t, true)
	ctx := context.Background()

	r, err := c.Register(ctx, receipt("s1"))
	if err != nil || r.GetFiscalNo() == "" {
		t.Fatalf("Register: %v %v", r, err)
	}
	// If the same sale comes again the same receipt number is returned (a second receipt is not issued).
	if again, err := c.Register(ctx, receipt("s1")); err != nil || again.GetFiscalNo() != r.GetFiscalNo() {
		t.Errorf("tekrarlanan Register: %v %v", again, err)
	}

	// If the device does not accept the basket no receipt has been issued (FailedPrecondition).
	b.mu.Lock()
	b.accept = "0"
	b.mu.Unlock()
	if _, err := c.Register(ctx, receipt("s2")); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("refused basket: %v", err)
	}
	// A VAT rate that is not mapped is an invalid request.
	bad := receipt("s3")
	bad.Receipt.Lines[0].TaxRateBp = 777
	if _, err := c.Register(ctx, bad); status.Code(err) != codes.InvalidArgument {
		t.Errorf("unmapped VAT: %v", err)
	}
}

func TestRegisterNotReady(t *testing.T) {
	c, _ := start(t, false)
	if _, err := c.Register(context.Background(), receipt("s1")); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("when not ready: %v", err)
	}
}
