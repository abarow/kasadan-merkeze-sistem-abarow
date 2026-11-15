// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"time"

	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/personnel"
	"abarow.com/services/store/internal/domain"
)

// Cashiers: the store manager manages the cashiers on the store server screen. A cashier
// is a role (pkg/personnel, decision 04.11.2026); the rule and the record are in pkg/personnel's
// Manage. With every change the person's latest state goes down to the registers (the store
// settings stream, with the PIN digest) and the change is reported to headquarters (the store
// stream, without the PIN digest) - both are written to the outbox in the same transaction
// as the change (Encoder).

// CashierChange is the record of a cashier change.
type CashierChange = personnel.Change

// SeedCashier is a cashier in the registers' cashier file (cashiers.json);
// once the file is loaded into the store server the cashiers are managed from here.
type SeedCashier = personnel.Seed

// Cashiers holds the use cases of cashier management; whoever does the operation
// is given by name (the manager with an open session or "store password").
type Cashiers struct {
	m *personnel.Manage
	// HashPIN produces the digest of the PIN to be stored (bcrypt; staff at the register
	// verifies with the same digest). Tests can set a fast digest.
	HashPIN func(pin string) ([]byte, error)
}

// NewCashiers returns a service.
func NewCashiers(store personnel.Store, enc personnel.Encoder) *Cashiers {
	m := personnel.NewManage(store, personnel.Config{
		Roles:   []personnel.Role{personnel.RoleCashier, personnel.RoleManager},
		ByName:  true,
		Encoder: enc,
		// A manager's session is tied to the PIN digest (Auth.Resolve): when the PIN changes
		// the old sessions close, the session of whoever changed their own PIN stays open.
		BoundSessions: true,
	})
	c := &Cashiers{m: m}
	c.HashPIN = m.HashPIN
	return c
}

// SetClock changes the service's clock (for tests).
func (s *Cashiers) SetClock(now func() time.Time) { s.m.SetClock(now) }

// actor is the session doing the operation: at the store server everyone with an open session (a manager
// or the store password) manages the cashiers.
func actor(by string) personnel.Actor { return personnel.Actor{Name: by, Manage: true} }

// manage returns the service that uses HashPIN if it has been changed.
func (s *Cashiers) manage() *personnel.Manage {
	s.m.HashPIN = s.HashPIN
	return s.m
}

// List returns the cashiers: open accounts first, in Turkish alphabet order.
func (s *Cashiers) List(ctx context.Context) ([]domain.Cashier, error) {
	return s.manage().List(ctx, actor(""))
}

// Names is the mapping from cashier ids to names (for the reports).
func (s *Cashiers) Names(ctx context.Context) (map[string]string, error) {
	return s.manage().Names(ctx)
}

// Get returns the cashier with their changes.
func (s *Cashiers) Get(ctx context.Context, id string) (domain.Cashier, []CashierChange, error) {
	return s.manage().Get(ctx, actor(""), id)
}

// Add adds a new cashier. email is the address by which the manager is recognized at the first
// sign-in with the corporate sign-in (it can be empty).
func (s *Cashiers) Add(ctx context.Context, by, id, name string, role domain.Role, pin, email string) (domain.Cashier, error) {
	return s.manage().Add(ctx, actor(by), id, pin, personnel.Input{Name: name, Role: role, Email: email})
}

// Update changes the cashier's name, role and email address.
func (s *Cashiers) Update(ctx context.Context, by, id, name string, role domain.Role, email string) (domain.Cashier, error) {
	return s.manage().Update(ctx, actor(by), id, personnel.Input{Name: name, Role: role, Email: email})
}

// SetEmail changes the email address by which the person is recognized at the first
// corporate sign-in (empty: removes it).
func (s *Cashiers) SetEmail(ctx context.Context, by, id, email string) (domain.Cashier, error) {
	return s.manage().SetEmail(ctx, actor(by), id, email)
}

// UnlinkIdentity removes the person's corporate identity link (they lost
// their identity or the IdP changed): at the next corporate sign-in the identity is matched
// by email again.
func (s *Cashiers) UnlinkIdentity(ctx context.Context, by, id string) (domain.Cashier, error) {
	return s.manage().Unlink(ctx, actor(by), id)
}

// People is the person record of the corporate sign-in (pkg/login/oidc).
func (s *Cashiers) People() oidc.People { return s.manage() }

// SetPIN changes the cashier's PIN; if locked at the register the lock is released.
func (s *Cashiers) SetPIN(ctx context.Context, by, id, pin string) (domain.Cashier, error) {
	return s.manage().SetSecret(ctx, actor(by), id, pin)
}

// SetDisabled closes or reopens the cashier's account. A closed cashier
// cannot sign in at the register, their open session closes.
func (s *Cashiers) SetDisabled(ctx context.Context, by, id string, disabled bool) (domain.Cashier, error) {
	return s.manage().SetDisabled(ctx, actor(by), id, disabled)
}

// Import loads the registers' cashier file: a missing cashier is added, an existing one's
// name, role and PIN change to what is in the file (a closed account stays closed).
// The whole file is checked first; if it is faulty none is loaded.
// It returns the number of cashiers loaded.
func (s *Cashiers) Import(ctx context.Context, by string, seeds []SeedCashier) (int, error) {
	return s.manage().Import(ctx, actor(by), seeds)
}
