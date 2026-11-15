// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package paymethod is the single definition of the payment types: their short names
// (databases, events and screens: "cash", "card" ...) and their numbers in the
// contracts. The PaymentMethod enumerations of the sales, payment, fiscal and staff
// contracts are the same; the translation between number and name is only
// here. A new payment type is added here and to the contracts.
package paymethod

const (
	Cash     = "cash"
	Card     = "card"
	MealCard = "meal_card" // meal card or voucher
	// Other is an unrecognized type (e.g. from a newer sales service):
	// the amount is not lost, it goes to the "Diğer" line in the reports.
	Other = "other"
)

// names are the short names of the numbers in the contracts (0: unspecified).
var names = map[int32]string{1: Cash, 2: Card, 3: MealCard}

// Name is the short name of the number in the contract; an unknown number returns "".
func Name(n int32) string { return names[n] }

// NameOrOther returns Other for an unknown number (reports: the amount is not lost).
func NameOrOther(n int32) string {
	if name, ok := names[n]; ok {
		return name
	}
	return Other
}

// Number is the short name's number in the contract; an unknown name returns 0
// (unspecified).
func Number(name string) int32 {
	for n, v := range names {
		if v == name {
			return n
		}
	}
	return 0
}
