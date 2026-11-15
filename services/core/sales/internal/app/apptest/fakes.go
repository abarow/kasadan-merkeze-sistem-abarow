// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package apptest holds the fake ports used in the sales tests.
package apptest

import (
	"context"
	"fmt"
	"slices"
	"time"

	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// Now is the fixed time used in tests.
var Now = time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)

// Config returns a service config that produces a fixed time and sequential ids (id-1,
// id-2, ...).
func Config() app.Config {
	n := 0
	return app.Config{
		StoreID:    "store-1",
		RegisterID: "register-1",
		Now:        func() time.Time { return Now },
		NewID: func() string {
			n++
			return fmt.Sprintf("id-%d", n)
		},
	}
}

// Payments is a fake PaymentGateway that counts the calls. It gives card payments
// references of the form REF-1, REF-2.
type Payments struct {
	Err   error
	Calls int
	// If RefundErr is given Refund fails; Refunded are the sales that were
	// refunded.
	RefundErr error
	Refunded  []domain.SaleID
}

func (p *Payments) Refund(_ context.Context, id domain.SaleID) error {
	if p.RefundErr != nil {
		return p.RefundErr
	}
	p.Refunded = append(p.Refunded, id)
	return nil
}

func (p *Payments) Charge(_ context.Context, _ domain.SaleID, payments []domain.Payment) ([]domain.Payment, error) {
	p.Calls++
	if p.Err != nil {
		return nil, p.Err
	}
	charged := slices.Clone(payments)
	for i := range charged {
		if charged[i].Method == domain.PaymentCard {
			charged[i].Ref = fmt.Sprintf("REF-%d", p.Calls)
		}
	}
	return charged, nil
}

// Fiscal is a fake FiscalDevice that returns numbers of the form SIM-000001, like the simulator.
type Fiscal struct {
	Err   error
	Calls int
	// Last is the sale received in the last Register call.
	Last *domain.Sale
	// During, if given, is called during Register (e.g. to cancel the request).
	During func(ctx context.Context)
}

func (f *Fiscal) Register(ctx context.Context, s *domain.Sale) (string, error) {
	f.Calls++
	f.Last = s
	if f.During != nil {
		f.During(ctx)
	}
	if f.Err != nil {
		return "", f.Err
	}
	return fmt.Sprintf("SIM-%06d", f.Calls), nil
}
