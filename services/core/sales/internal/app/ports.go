// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"

	"abarow.com/pkg/outbox"
	"abarow.com/services/core/sales/internal/domain"
)

// ErrNotFound is returned when the sale is not found.
var ErrNotFound = errors.New("satış bulunamadı")

// ErrNotProcessed: the fiscal device did not issue the receipt and did not take the payment (if
// any): the device definitely did not perform the operation (e.g. the card was declined, the
// device is not ready, the receipt is invalid). In a sale whose payment the device takes
// (Config.FiscalCollects) the sale stays open, the cashier continues with another payment.
// Errors whose result is uncertain (the connection broke, no result came) are not this:
// the payment may have been taken.
var ErrNotProcessed = errors.New("ÖKC fişi kesmedi")

// ErrCollectedByDevice: the payment of a sale whose payment was taken by the fiscal device
// cannot be refunded from the payment service; the refund is done through the device.
var ErrCollectedByDevice = errors.New("ödeme mali cihazca alındı; payment servisinden geri verilemez")

// Repository keeps the sales permanently.
type Repository interface {
	// Get returns the sale; ErrNotFound if none.
	Get(ctx context.Context, id domain.SaleID) (*domain.Sale, error)
	// InTx runs fn in a single transaction. If fn returns an error no change
	// becomes permanent.
	InTx(ctx context.Context, fn func(tx Tx) error) error
	// ByStatus returns at most limit of the sales in the given status from newest
	// to oldest.
	ByStatus(ctx context.Context, status domain.Status, limit int) ([]*domain.Sale, error)
}

// Tx is the operations that can be done within a transaction.
type Tx interface {
	// Get returns the sale; ErrNotFound if none.
	Get(ctx context.Context, id domain.SaleID) (*domain.Sale, error)
	// Save adds or updates the sale.
	Save(ctx context.Context, s *domain.Sale) error
	// NextReceiptNo allocates the register's next receipt number.
	NextReceiptNo(ctx context.Context) (int64, error)
	// AppendOutbox writes the events to the outbox; a separate relay passes them on to NATS.
	AppendOutbox(ctx context.Context, msgs []OutboxMessage) error
}

// PaymentGateway, payment servisidir.
type PaymentGateway interface {
	// Charge takes the payments (change for cash, the bank POS for card) and
	// returns each payment with the Ref field filled in.
	//
	// It must be idempotent by sale id: if called again for the same sale it
	// does not take a new payment, it returns the earlier result. So a retry after
	// the payment was taken and the answer was lost does not charge the customer a
	// second time.
	Charge(ctx context.Context, id domain.SaleID, payments []domain.Payment) ([]domain.Payment, error)
	// Refund gives back the sale's payments that were taken (card transactions are
	// reversed, the cashier gives the cash). It must be callable again.
	Refund(ctx context.Context, id domain.SaleID) error
}

// FiscalDevice is the active fiscal adapter (simulator or ÖKC). It prints the receipt.
type FiscalDevice interface {
	// Register records the sale fiscally and returns the fiscal number.
	Register(ctx context.Context, s *domain.Sale) (fiscalNo string, err error)
}

// OutboxMessage is an event written to the outbox and later passed on to NATS
// (see pkg/outbox).
type OutboxMessage = outbox.Message

// EventEncoder encodes the event for the message path and returns the content type. id
// is the event's id, storeID is the store; the contract can carry these too.
type EventEncoder interface {
	Encode(e domain.Event, id, storeID string) (payload []byte, contentType string, err error)
}
