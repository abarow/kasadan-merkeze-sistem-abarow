// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package terminalsim is the app.CardTerminal adapter that imitates a bank POS.
// It is for development and Docker simulation; there is no real money
// movement.
package terminalsim

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"abarow.com/pkg/money"
	"abarow.com/services/core/payment/internal/app"
)

// Terminal approves every charge and gives a reference of the form SIMPOS-000001.
// If the limit is greater than zero charges that exceed the limit are rejected (for the
// insufficient balance scenario).
type Terminal struct {
	Limit money.Money
	Log   *slog.Logger
	n     atomic.Int64
}

var _ app.CardTerminal = (*Terminal)(nil)

func (t *Terminal) Charge(_ context.Context, saleID string, amount money.Money) (string, error) {
	if t.Limit > 0 && amount > t.Limit {
		t.log().Info("POS simülatörü: reddedildi", "sale_id", saleID, "amount", amount.String())
		return "", fmt.Errorf("limit aşıldı: %w", app.ErrDeclined)
	}
	ref := fmt.Sprintf("SIMPOS-%06d", t.n.Add(1))
	t.log().Info("POS simülatörü: onaylandı", "sale_id", saleID, "amount", amount.String(), "ref", ref)
	return ref, nil
}

func (t *Terminal) Reverse(_ context.Context, ref string) error {
	t.log().Info("POS simülatörü: işlem geri alındı", "ref", ref)
	return nil
}

func (t *Terminal) log() *slog.Logger {
	if t.Log == nil {
		return slog.Default()
	}
	return t.Log
}
