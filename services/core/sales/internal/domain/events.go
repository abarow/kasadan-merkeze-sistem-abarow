// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"time"

	"abarow.com/pkg/money"
)

// Event is a change in the sale that will be announced to the outside.
type Event interface {
	// Name is the versioned name of the event (sale.completed.v1); it is used as the NATS
	// subject too.
	Name() string
	// OccurredAt is the moment the event occurred.
	OccurredAt() time.Time
}

// SaleCompleted announces a sale whose payment was taken and fiscal record made.
type SaleCompleted struct {
	SaleID     SaleID      `json:"sale_id"`
	RegisterID string      `json:"register_id"`
	CashierID  string      `json:"cashier_id,omitempty"`
	ReceiptNo  int64       `json:"receipt_no"`
	FiscalNo   string      `json:"fiscal_no"`
	Total      money.Money `json:"total_kurus"`
	TaxTotal   money.Money `json:"tax_total_kurus"`
	Change     money.Money `json:"change_kurus"`
	Taxes      []TaxLine   `json:"taxes"`
	Lines      []Line      `json:"lines"`
	Payments   []Payment   `json:"payments"`
	At         time.Time   `json:"occurred_at"`
}

func (SaleCompleted) Name() string            { return "sale.completed.v1" }
func (e SaleCompleted) OccurredAt() time.Time { return e.At }

// SaleCharged announces a sale whose payment was taken but whose fiscal record (receipt)
// could not be made. It is not published on a successful payment; only when the fiscal record
// fails (at every attempt). The sale waits until it is completed at the register or its
// payment is refunded and it is cancelled; the store server shows these.
type SaleCharged struct {
	SaleID     SaleID      `json:"sale_id"`
	RegisterID string      `json:"register_id"`
	CashierID  string      `json:"cashier_id,omitempty"`
	Total      money.Money `json:"total_kurus"`
	Lines      []Line      `json:"lines"`
	Payments   []Payment   `json:"payments"`
	// Reason is why the fiscal record could not be made.
	Reason string    `json:"reason"`
	At     time.Time `json:"occurred_at"`
}

func (SaleCharged) Name() string            { return "sale.charged.v1" }
func (e SaleCharged) OccurredAt() time.Time { return e.At }

// SaleVoided announces a sale cancelled without its payment being taken.
type SaleVoided struct {
	SaleID     SaleID      `json:"sale_id"`
	RegisterID string      `json:"register_id"`
	CashierID  string      `json:"cashier_id,omitempty"`
	Total      money.Money `json:"total_kurus"`
	// Refunded: the payment had been taken, it was refunded because the fiscal record could not be made.
	Refunded bool      `json:"refunded,omitempty"`
	At       time.Time `json:"occurred_at"`
}

func (SaleVoided) Name() string            { return "sale.voided.v1" }
func (e SaleVoided) OccurredAt() time.Time { return e.At }
