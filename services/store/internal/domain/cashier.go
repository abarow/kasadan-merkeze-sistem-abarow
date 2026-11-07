// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"abarow.com/pkg/personnel"
)

// Cashiers are managed on the store server screen: the store manager adds a cashier,
// changes their name, role and PIN, closes the account of whoever leaves the job.
// A cashier is not deleted; their id stays in the sales and shifts.

// The cashier rules (code, name, role, PIN) are in pkg/personnel; the register and headquarters
// use the same ones.

// Role, kasiyerin yetkisidir.
type Role = personnel.Role

const (
	RoleCashier = personnel.RoleCashier
	// RoleManager takes the end of day and gives manager approval at the register.
	RoleManager = personnel.RoleManager
)

const (
	// MaxCashierIDLen and MaxCashierNameLen are the length limits of the code and the name.
	MaxCashierIDLen   = personnel.MaxIDLen
	MaxCashierNameLen = personnel.MaxNameLen
)

var (
	ErrInvalidCashierID = personnel.ErrInvalidID
	ErrInvalidName      = personnel.ErrInvalidName
	ErrInvalidRole      = personnel.ErrInvalidRole
	ErrInvalidPIN       = personnel.ErrInvalidPIN
	ErrCashierNotFound  = personnel.ErrNotFound
	ErrCashierExists    = personnel.ErrExists
	ErrInvalidEmail     = personnel.ErrInvalidEmail
	ErrEmailTaken       = personnel.ErrEmailTaken
)

// Cashier is a cashier managed at the store server: pkg/personnel's single person
// model (a cashier is a role). The PIN is stored only as its one-way digest (bcrypt);
// the digest also goes to the registers.
type Cashier = personnel.User

// ValidateCashierID checks that the code can be used in a NATS subject and at the register:
// a-z, 0-9, - and _.
func ValidateCashierID(id string) error { return personnel.ValidateID(id) }

// CleanName trims the spaces at the start and end of the name and checks it.
func CleanName(name string) (string, error) { return personnel.CleanName(name) }

// ValidateRole checks that the role is a known role.
func ValidateRole(r Role) error { return personnel.ValidateRole(r) }

// ValidatePIN checks that the PIN is a 4-8 digit number (the same as the rule
// at the register).
func ValidatePIN(pin string) error { return personnel.ValidatePIN(pin) }

// RoleName is the name of the role shown on the screen and in the change record.
func RoleName(r Role) string { return personnel.RoleName(r) }
