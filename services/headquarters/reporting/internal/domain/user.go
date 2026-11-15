// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import "abarow.com/pkg/personnel"

// Headquarters users are pkg/personnel's single person model (decision
// 04.11.2026: a cashier, a manager and a headquarters user are all the same User, the difference
// is the role). The rules and errors are there; here are the
// names headquarters uses.

// Role is the user's permission (decision 29.10.2026): admin (administrator),
// catalog or report.
type Role = personnel.Role

const (
	// RoleAdmin manages users, changes the catalog and sees all the
	// stores.
	RoleAdmin = personnel.RoleAdmin
	// RoleCatalog changes products and headquarters prices; in the reports sees the
	// stores they are allowed.
	RoleCatalog = personnel.RoleCatalog
	// RoleReport sees the stores they are allowed in the reports; only reads the
	// catalog.
	RoleReport = personnel.RoleReport
)

// User is a user of the headquarters screen. Every person has their own account.
// The administrator sees all the stores; the others see all the stores
// (AllStores) or only those in Stores.
type User = personnel.User

var (
	// ErrInvalidUsername means the username is invalid.
	ErrInvalidUsername = personnel.ErrInvalidUsername
	// ErrPasswordTooShort means the password is too short.
	ErrPasswordTooShort = personnel.ErrPasswordTooShort
	// ErrInvalidName means the person's name is invalid.
	ErrInvalidName = personnel.ErrInvalidName
	// ErrInvalidRole means the role is invalid.
	ErrInvalidRole = personnel.ErrInvalidRole
	// ErrRoleNotManaged means the role is not managed here (e.g. cashier).
	ErrRoleNotManaged = personnel.ErrRoleNotManaged
	// ErrInvalidStore means the store id is invalid.
	ErrInvalidStore = personnel.ErrInvalidStore
	// ErrNoStores means no store was given to a user who is not an administrator.
	ErrNoStores = personnel.ErrNoStores
	// ErrInvalidEmail means the email address is invalid.
	ErrInvalidEmail = personnel.ErrInvalidEmail
	// ErrEmailTaken means the email address is registered to another user.
	ErrEmailTaken = personnel.ErrEmailTaken
	// ErrUserNotFound means the user does not exist.
	ErrUserNotFound = personnel.ErrNotFound
	// ErrUserExists means the user already exists.
	ErrUserExists = personnel.ErrExists
	// ErrForbidden means the user has no permission for this operation.
	ErrForbidden = personnel.ErrForbidden
	// ErrSelf means the administrator locking themselves out.
	ErrSelf = personnel.ErrSelf
	// ErrLastAdmin means closing the last administrator with an open account or changing their
	// role: no one would be left to manage the users on the screen.
	ErrLastAdmin = personnel.ErrLastAdmin
)

// Username lower-cases the username and checks it.
func Username(s string) (string, error) { return personnel.Username(s) }

// ValidatePassword checks that the password is long enough.
func ValidatePassword(p string) error { return personnel.ValidatePassword(p) }

// RoleName is the name of the role shown on the screen and in the records.
func RoleName(r Role) string { return personnel.RoleName(r) }

// MaxNameLen is the longest length of a person's name.
const MaxNameLen = personnel.MaxNameLen
