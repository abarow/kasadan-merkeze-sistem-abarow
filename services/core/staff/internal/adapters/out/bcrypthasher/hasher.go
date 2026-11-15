// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package bcrypthasher is the app.PINHasher adapter that digests PINs with bcrypt.
// bcrypt is slow on purpose; it makes finding short PINs by trial and error harder
// (the real protection is the wrong-attempt lock).
package bcrypthasher

import (
	"golang.org/x/crypto/bcrypt"

	"abarow.com/services/core/staff/internal/app"
)

// Hasher implements app.PINHasher. bcrypt.DefaultCost is used if Cost is zero.
type Hasher struct {
	Cost int
}

var _ app.PINHasher = Hasher{}

func (h Hasher) Hash(pin string) ([]byte, error) {
	cost := h.Cost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	return bcrypt.GenerateFromPassword([]byte(pin), cost)
}

func (h Hasher) Matches(hash []byte, pin string) bool {
	return bcrypt.CompareHashAndPassword(hash, []byte(pin)) == nil
}
