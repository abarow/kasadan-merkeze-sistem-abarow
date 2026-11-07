// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package paymethod_test

import (
	"testing"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"
	paymentv1 "abarow.com/gen/com/abarow/payment/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/paymethod"
)

// The enumerations of the four contracts must stay the same: since the translation is
// done in one place, if one diverges this test fails.
func TestContractsAgree(t *testing.T) {
	for _, name := range []string{paymethod.Cash, paymethod.Card, paymethod.MealCard} {
		n := paymethod.Number(name)
		if n == 0 || paymethod.Name(n) != name {
			t.Fatalf("%s: numara %d", name, n)
		}
		for contract, got := range map[string]string{
			"sales":   salesv1.PaymentMethod(n).String(),
			"payment": paymentv1.PaymentMethod(n).String(),
			"fiscal":  fiscalv1.PaymentMethod(n).String(),
			"staff":   staffv1.PaymentMethod(n).String(),
		} {
			if want := salesv1.PaymentMethod(n).String(); got != want {
				t.Errorf("in the %s contract %d = %s, in sales %s", contract, n, got, want)
			}
		}
	}
	if paymethod.Name(99) != "" || paymethod.NameOrOther(99) != paymethod.Other || paymethod.Number("altin") != 0 {
		t.Error("unknown kind")
	}
}
