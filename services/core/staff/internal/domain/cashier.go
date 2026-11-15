// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"fmt"
	"math"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel"
)

// Role is the cashier's authority (pkg/personnel; the same as the store server and headquarters).
type Role = personnel.Role

const (
	RoleCashier = personnel.RoleCashier
	// RoleManager can take the end of day (Z).
	RoleManager = personnel.RoleManager
)

// Origin is where the cashier came from.
type Origin string

const (
	// FromSeed was loaded from the cashier file in the install (cashiers.json).
	FromSeed Origin = "seed"
	// FromStore is managed on the store server screen; the cashier file does not
	// change it.
	FromStore Origin = "store"
)

// The lock rule is in pkg/login (the same code as the store server and headquarters).
var (
	// MaxFailedAttempts is the number of successive wrong PINs before locking.
	MaxFailedAttempts = login.Default.MaxFailures
	// LockDuration is the lock time after the wrong attempts.
	LockDuration = login.Default.Lock
)

var (
	ErrInvalidPIN     = personnel.ErrInvalidPIN
	ErrInvalidRole    = personnel.ErrInvalidRole
	ErrWrongPIN       = errors.New("PIN hatalı")
	ErrLocked         = errors.New("kasiyer kilitli")
	ErrUnknownCashier = errors.New("kasiyer bulunamadı")
	ErrInvalidSession = errors.New("oturum geçersiz ya da süresi dolmuş; yeniden giriş yapın")
	ErrDisabled       = errors.New("kasiyerin hesabı kapalı; mağaza müdürüne başvurun")
	ErrInvalidCashier = errors.New("kasiyer bilgisi eksik")
)

// Cashier is a person who works at the register. The PIN is stored only as a one-way
// digest.
type Cashier struct {
	ID             string
	Name           string
	Role           Role
	PINHash        []byte
	FailedAttempts int
	LockedUntil    time.Time
	// Disabled means the account is closed (a cashier who left the job): they cannot
	// sign in, their open sessions close. Their record in the sales is not deleted.
	Disabled bool
	Origin   Origin
}

// ValidatePIN checks that the PIN is a 4–8 digit number.
func ValidatePIN(pin string) error { return personnel.ValidatePIN(pin) }

// ValidateRole checks that the role is a known role.
func ValidateRole(r Role) error { return personnel.ValidateRole(r) }

// Locked says whether the cashier is locked at the moment now.
func (c *Cashier) Locked(now time.Time) bool { return c.tries().Locked(now) }

// RecordFailure counts the wrong attempt and returns the remaining attempts.
// When the limit is reached the cashier is locked for LockDuration.
func (c *Cashier) RecordFailure(now time.Time) (remaining int) {
	t := c.tries()
	remaining = t.Fail(now, login.Default)
	c.FailedAttempts, c.LockedUntil = t.Failures, t.LockedUntil
	return remaining
}

// tries is the cashier's attempt record (the rule is in pkg/login).
func (c *Cashier) tries() login.Tries {
	return login.Tries{Failures: c.FailedAttempts, LockedUntil: c.LockedUntil}
}

// RecordSuccess resets the counter and the lock on a successful sign-in.
func (c *Cashier) RecordSuccess() {
	c.FailedAttempts = 0
	c.LockedUntil = time.Time{}
}

// CanCloseDay says whether the cashier can take the end of day (Z).
func (c Cashier) CanCloseDay() bool { return c.Role == RoleManager }

// WrongPINError carries the wrong PIN and the remaining attempts.
type WrongPINError struct{ Remaining int }

func (e WrongPINError) Error() string {
	if e.Remaining == 0 {
		return fmt.Sprintf("PIN hatalı; kasiyer %d dakika kilitlendi", int(LockDuration.Minutes()))
	}
	return fmt.Sprintf("PIN hatalı; %d deneme hakkı kaldı", e.Remaining)
}

func (e WrongPINError) Unwrap() error { return ErrWrongPIN }

// LockedError carries how long the lock will last.
type LockedError struct{ Remaining time.Duration }

func (e LockedError) Error() string {
	return fmt.Sprintf("kasiyer kilitli; %d dakika sonra tekrar deneyin", int(math.Ceil(e.Remaining.Minutes())))
}

func (e LockedError) Unwrap() error { return ErrLocked }

// Session is an open cashier session. Not the session key itself but its SHA-256
// digest is stored; even if the database leaks the sessions cannot be hijacked.
type Session struct {
	TokenHash string
	CashierID string
	ExpiresAt time.Time
}

// Expired says whether the session has expired at the moment now.
func (s Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }
