// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the business rules of the headquarters reports: the stores'
// state, the store prices and the screen users. The period summary is in the shared report
// core (pkg/report).
package domain

import "abarow.com/pkg/paymethod"

// The short names of the payment types in the sales contract.
const (
	Cash     = paymethod.Cash
	Card     = paymethod.Card
	MealCard = paymethod.MealCard
)

// MaxProducts is the largest number of products shown in the summary.
const MaxProducts = 50
