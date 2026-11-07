// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the payment concepts and rules.
package domain

import (
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

// Method is the payment method.
type Method string

const (
	Cash     Method = paymethod.Cash
	Card     Method = paymethod.Card
	MealCard Method = paymethod.MealCard // meal card or voucher
)

// OnTerminal says the amount is charged from the POS (card, meal card); cash
// is in the cashier's hand.
func (m Method) OnTerminal() bool { return m == Card || m == MealCard }

// Label is the name shown to the cashier.
func (m Method) Label() string {
	switch m {
	case Cash:
		return "nakit"
	case Card:
		return "kart"
	case MealCard:
		return "yemek kartı"
	}
	return string(m)
}

var ErrInvalidRequest = errors.New("geçersiz ödeme isteği")

// Payment is a payment.
type Payment struct {
	Method Method      `json:"method"`
	Amount money.Money `json:"amount_kurus"`
	// Ref is the reference number of the POS transaction (card, meal card); empty
	// for cash.
	Ref string `json:"ref,omitempty"`
	// Refunded shows that the payment was given back: the card transaction was
	// reversed or the cash was returned to the customer by the cashier.
	Refunded bool `json:"refunded,omitempty"`
}

// Charge is the payments taken for a sale. There is a single record per sale;
// repeated requests for the same sale are answered with this record.
type Charge struct {
	SaleID   string
	Payments []Payment
	At       time.Time
}

// ValidateRequest checks the payment request. Whether the amount covers the sale is
// sales' rule; only the format of the request is checked here.
func ValidateRequest(saleID string, payments []Payment) error {
	if saleID == "" {
		return fmt.Errorf("%w: satış kimliği boş", ErrInvalidRequest)
	}
	if len(payments) == 0 {
		return fmt.Errorf("%w: ödeme yok", ErrInvalidRequest)
	}
	for _, p := range payments {
		if p.Amount <= 0 {
			return fmt.Errorf("%w: tutar pozitif olmalı", ErrInvalidRequest)
		}
		if p.Method != Cash && !p.Method.OnTerminal() {
			return fmt.Errorf("%w: bilinmeyen yöntem %q", ErrInvalidRequest, p.Method)
		}
	}
	return nil
}
