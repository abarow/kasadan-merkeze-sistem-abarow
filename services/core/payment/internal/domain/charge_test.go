// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
)

func TestMethod(t *testing.T) {
	tests := []struct {
		m        Method
		terminal bool
		label    string
	}{
		{Cash, false, "nakit"},
		{Card, true, "kart"},
		{MealCard, true, "yemek kartı"},
		{Method("çek"), false, "çek"},
	}
	for _, tc := range tests {
		if tc.m.OnTerminal() != tc.terminal {
			t.Errorf("%q: OnTerminal = %v", tc.m, tc.m.OnTerminal())
		}
		if tc.m.Label() != tc.label {
			t.Errorf("%q: Label = %q", tc.m, tc.m.Label())
		}
	}
}

func TestValidateRequest(t *testing.T) {
	cash := Payment{Method: Cash, Amount: 1000}
	tests := []struct {
		name     string
		saleID   string
		payments []Payment
		ok       bool
	}{
		{"nakit", "s1", []Payment{cash}, true},
		{"karışık", "s1", []Payment{cash, {Method: Card, Amount: 500}, {Method: MealCard, Amount: 1}}, true},
		{"satış kimliği yok", "", []Payment{cash}, false},
		{"ödeme yok", "s1", nil, false},
		{"sıfır tutar", "s1", []Payment{{Method: Cash}}, false},
		{"eksi tutar", "s1", []Payment{{Method: Cash, Amount: -1}}, false},
		{"bilinmeyen yöntem", "s1", []Payment{{Method: "çek", Amount: 100}}, false},
	}
	for _, tc := range tests {
		err := ValidateRequest(tc.saleID, tc.payments)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: kabul edildi: %v", tc.name, err)
		}
	}
}
