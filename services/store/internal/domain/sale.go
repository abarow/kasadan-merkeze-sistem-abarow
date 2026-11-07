// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import "abarow.com/pkg/ledger"

// The model of the sale events is in the sales ledger (pkg/ledger); headquarters uses
// the same types.
type (
	Sale    = ledger.Sale
	Line    = ledger.Line
	Payment = ledger.Payment
	Tax     = ledger.Tax
	Void    = ledger.Void
	Shift   = ledger.Shift
)

// Payment types (with their names in the sales contract).
const (
	Cash     = ledger.Cash
	Card     = ledger.Card
	MealCard = ledger.MealCard
)

// ErrInvalidEvent is an event too incomplete to be taken into the report.
var ErrInvalidEvent = ledger.ErrInvalidEvent
