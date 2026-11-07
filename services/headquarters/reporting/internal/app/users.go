// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"abarow.com/pkg/personnel"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// User management: the administrator adds users from the screen, and at first install
// the install script from the command line; changes their name, role, stores and
// password, closes the account. Every change is recorded with who made it and
// a readable summary. The administrator cannot lock themselves out; the last administrator with an open
// account cannot be closed and their role cannot be changed.

// Installer is the maker of the changes done from the command line (the install
// script); it has administrator permission.
var Installer = domain.User{Name: "kurulum (komut satırı)", Role: domain.RoleAdmin, AllStores: true}

// UserInput is the details of the user that can be changed.
type UserInput = personnel.Input

// actor is the management permission of the user doing the operation: only the administrator
// manages users.
func actor(u domain.User) personnel.Actor {
	return personnel.Actor{ID: u.ID, Name: u.Name, Manage: u.CanManageUsers()}
}

// Users returns the users.
func (a *Auth) Users(ctx context.Context, by domain.User) ([]domain.User, error) {
	return a.people.List(ctx, actor(by))
}

// User returns the user with their changes.
func (a *Auth) User(ctx context.Context, by domain.User, username string) (domain.User, []UserChange, error) {
	return a.people.Get(ctx, actor(by), username)
}

// AddUser adds a user. If the name is empty the username is used.
func (a *Auth) AddUser(ctx context.Context, by domain.User, username, password string, in UserInput) (domain.User, error) {
	return a.people.Add(ctx, actor(by), username, password, in)
}

// UpdateUser changes the user's name, role and stores.
func (a *Auth) UpdateUser(ctx context.Context, by domain.User, username string, in UserInput) (domain.User, error) {
	return a.people.Update(ctx, actor(by), username, in)
}

// SetPassword changes the user's password; their open sessions close.
func (a *Auth) SetPassword(ctx context.Context, by domain.User, username, password string) error {
	_, err := a.people.SetSecret(ctx, actor(by), username, password)
	return err
}

// SetDisabled closes the account (the sessions close too) or reopens it.
func (a *Auth) SetDisabled(ctx context.Context, by domain.User, username string, disabled bool) (domain.User, error) {
	return a.people.SetDisabled(ctx, actor(by), username, disabled)
}

// SetEmail changes the email address by which the user is recognized at the first
// corporate sign-in (empty: removes it).
func (a *Auth) SetEmail(ctx context.Context, by domain.User, username, email string) (domain.User, error) {
	return a.people.SetEmail(ctx, actor(by), username, email)
}

// UnlinkIdentity removes the user's corporate identity link (the person lost
// their identity or the IdP changed): at the next corporate sign-in the
// identity is matched by email again.
func (a *Auth) UnlinkIdentity(ctx context.Context, by domain.User, username string) (domain.User, error) {
	return a.people.Unlink(ctx, actor(by), username)
}
