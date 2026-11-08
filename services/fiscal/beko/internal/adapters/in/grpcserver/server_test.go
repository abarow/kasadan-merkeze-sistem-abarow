// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/services/fiscal/beko/internal/app"
	"abarow.com/services/fiscal/beko/internal/domain"
)

func TestCode(t *testing.T) {
	for err, want := range map[error]codes.Code{
		app.ErrNotReady:                           codes.FailedPrecondition,
		app.ErrNoResult:                           codes.Unavailable,
		app.ErrRejected:                           codes.FailedPrecondition,
		app.ErrDeclined:                           codes.FailedPrecondition,
		app.ErrVoided:                             codes.Aborted,
		domain.ErrUnmappedTax:                     codes.InvalidArgument,
		domain.ErrCardNotCollected:                codes.InvalidArgument,
		domain.ErrInvalidReceipt:                  codes.InvalidArgument,
		errors.New("beklenmeyen"):                 codes.Unavailable,
		fmt.Errorf("sarılı: %w", app.ErrDeclined): codes.FailedPrecondition,
	} {
		if got := code(err); got != want {
			t.Errorf("%v → %v, want %v", err, got, want)
		}
	}
}

func TestFromProto(t *testing.T) {
	r := fromProto(&fiscalv1.Receipt{
		SaleId: "s1", TotalKurus: 1500, ChangeKurus: 500,
		Lines:    []*fiscalv1.ReceiptLine{{Name: "Ekmek", QtyMilli: 1000, UnitPriceKurus: 1500, TotalKurus: 1500, TaxRateBp: 1000}},
		Payments: []*fiscalv1.Payment{{Method: fiscalv1.PaymentMethod_PAYMENT_METHOD_MEAL_CARD, AmountKurus: 2000, Collect: true}},
	})
	if r.SaleID != "s1" || r.Total != 1500 || r.Change != 500 || len(r.Lines) != 1 || r.Lines[0].TaxRate != 1000 ||
		len(r.Payments) != 1 || r.Payments[0].Method != "meal_card" || r.Payments[0].Amount != 2000 {
		t.Errorf("receipt: %+v", r)
	}
}
