// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the business rules of headquarters' ingest service: the events
// coming from the stores and the stores' connection state.
package domain

import (
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
)

// ErrInvalidEvent is an event too incomplete to be recorded.
var ErrInvalidEvent = ledger.ErrInvalidEvent

// Event is the raw form of an event coming from a store: its id (ce-id), kind,
// source and encoded content. Headquarters stores every event in its raw form first;
// an event that comes again is de-duplicated by its id. The raw record also serves to
// reprocess the events if the read models change.
type Event struct {
	ID, Subject         string
	StoreID, RegisterID string
	OccurredAt          time.Time
	ContentType         string
	Payload             []byte
}

// Validate checks that the event can be stored.
func (e Event) Validate() error {
	if e.ID == "" || e.Subject == "" || e.StoreID == "" || e.OccurredAt.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

// The model of the sale and shift events is in the sales ledger (pkg/ledger);
// the store server uses the same types. At headquarters the store id is always
// filled (the store the event came from).
type (
	Sale       = ledger.Sale
	Line       = ledger.Line
	Payment    = ledger.Payment
	Tax        = ledger.Tax
	Void       = ledger.Void
	Unfinished = ledger.Unfinished
	Shift      = ledger.Shift
)

// The short names of the payment types in the sales contract; an unknown type comes with its
// own name.
const (
	Cash     = ledger.Cash
	Card     = ledger.Card
	MealCard = ledger.MealCard
)

// InStore checks that the ledger record can be stored at headquarters: the record's
// own rules (err, the result of Validate) and the store id. A record with no store
// id is not stored without knowing whom it belongs to.
func InStore(storeID string, err error) error {
	if err == nil && storeID == "" {
		return ErrInvalidEvent
	}
	return err
}

// StorePrices is a store's price schedule for a product (the store
// exception; store.price.changed.v1). The store reports the latest state of the schedule
// with every change; an empty schedule means there is no exception.
type StorePrices struct {
	StoreID, ProductID, ProductName string
	Prices                          []StorePrice
	UpdatedBy                       string
	At                              time.Time
}

// StorePrice is a record of the store schedule: the price valid from the moment From
// (from the beginning if From is zero); Clear means that from that moment the
// exception is lifted and the headquarters price becomes valid.
type StorePrice struct {
	Amount money.Money
	From   time.Time
	Clear  bool
}

// Validate checks that the store price can be recorded.
func (s StorePrices) Validate() error {
	if s.StoreID == "" || s.ProductID == "" || s.At.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}

// Cashier is the latest state of a cashier a store manages on the store server
// (store.cashier.changed.v1). The PIN does not come to headquarters.
type Cashier struct {
	StoreID, CashierID, Name string
	// Role: cashier or manager.
	Role      string
	Disabled  bool
	UpdatedBy string
	At        time.Time
}

// Validate checks that the cashier can be recorded.
func (c Cashier) Validate() error {
	if c.StoreID == "" || c.CashierID == "" || c.Name == "" || c.At.IsZero() {
		return ErrInvalidEvent
	}
	return nil
}
