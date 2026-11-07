// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personnel

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The roles of headquarters users (decision 29.10.2026). The roles of store
// staff are RoleCashier and RoleManager (personnel.go).
const (
	// RoleAdmin manages users, changes the catalog and sees all the
	// stores.
	RoleAdmin Role = "admin"
	// RoleCatalog changes products and headquarters prices; in the reports sees the
	// stores they are allowed.
	RoleCatalog Role = "catalog"
	// RoleReport sees the stores they are allowed in the reports; only reads the
	// catalog.
	RoleReport Role = "report"
)

// Credential is the kind of credential the role signs in with.
type Credential int

const (
	// CredentialPIN is the numeric PIN entered at the register and the store server.
	CredentialPIN Credential = iota + 1
	// CredentialPassword is the password entered at headquarters.
	CredentialPassword
)

const (
	// MinPasswordLen is the shortest length of the password. Since headquarters
	// may be reachable from the internet it is longer than the store
	// password.
	MinPasswordLen = 10
	maxScopeStores = 1000
)

var roleTable = map[Role]struct {
	credential Credential
	store      bool
}{
	RoleCashier: {CredentialPIN, true},
	RoleManager: {CredentialPIN, true},
	RoleAdmin:   {CredentialPassword, false},
	RoleCatalog: {CredentialPassword, false},
	RoleReport:  {CredentialPassword, false},
}

// Known says the role is a known role.
func (r Role) Known() bool { _, ok := roleTable[r]; return ok }

// Credential is the role's credential kind; 0 for an unknown role.
func (r Role) Credential() Credential { return roleTable[r].credential }

// StoreStaff says the role is a store's staff (cashier, manager): its scope is the
// store itself, it goes down to the register.
func (r Role) StoreStaff() bool { return roleTable[r].store }

var (
	// ErrInvalidUsername means the username of a role with a password is invalid.
	ErrInvalidUsername = errors.New("kullanıcı adı geçersiz: küçük harf (a-z), rakam, nokta, - ve _; 2–32 karakter")
	// ErrPasswordTooShort means the password is too short.
	ErrPasswordTooShort = fmt.Errorf("parola en az %d karakter olmalı", MinPasswordLen)
	// ErrInvalidStore means the store id is invalid.
	ErrInvalidStore = errors.New("mağaza kimliği geçersiz: harf, rakam, - ve _")
	// ErrNoStores means no store was given to a user who does not have store scope.
	ErrNoStores = errors.New("mağaza seçilmeli: bütün mağazalar ya da en az bir mağaza")
	// ErrInvalidEmail means the email address is invalid.
	ErrInvalidEmail = errors.New("e-posta adresi geçersiz")
)

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)
	storePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
)

// User is a person. The same model at both tiers: store staff
// (cashier, manager) are managed at the store server, headquarters users at
// headquarters. The credential is stored only as its one-way digest (bcrypt).
type User struct {
	// ID is the cashier code or username.
	ID, Name string
	Role     Role
	// If AllStores is false the headquarters user sees only the stores in Stores.
	// It is not used for store staff.
	AllStores bool
	Stores    []string
	// Email is the address by which the person will be recognized at the first sign-in with the
	// corporate sign-in (OIDC); it is the administrator's predefinition (lower case). It gives no
	// sign-in authority: the IdP proves the identity, the person is selected from this record.
	Email string
	// Issuer and Subject are the corporate identity linked to the person (the IdP's address and
	// the unchanging person id at that IdP); empty if not linked.
	Issuer, Subject string
	// Disabled means the account is closed (one who left the job): they cannot sign in.
	// The person is not deleted; their id stays in the sales and shifts.
	Disabled bool
	// PINHash is the PIN digest of the roles with a PIN; PasswordHash is the
	// password digest of the roles with a password.
	PINHash      []byte
	PasswordHash string
	CreatedAt    time.Time
	UpdatedBy    string
	UpdatedAt    time.Time
}

// Username lower-cases and checks the username of a role with a password.
// The name is ASCII only: so that Turkish upper/lower-case conversion does not cause
// confusion at sign-in (both I and İ become i; ı is invalid). For the code of roles with a PIN
// ValidateID.
func Username(s string) (string, error) {
	u := strings.ToLower(strings.TrimSpace(s))
	if !usernamePattern.MatchString(u) {
		return "", ErrInvalidUsername
	}
	return u, nil
}

// CleanEmail checks the email address and lower-cases it; an empty address means
// "none".
func CleanEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", ErrInvalidEmail
	}
	return s, nil
}

// ValidatePassword checks that the password is long enough.
func ValidatePassword(p string) error {
	if len([]rune(p)) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

// CleanID checks and normalizes the role's id (code or username).
func CleanID(role Role, id string) (string, error) {
	if role.Credential() == CredentialPassword {
		return Username(id)
	}
	return id, ValidateID(id)
}

// ValidateScope checks and normalizes the user's role and stores:
// the store list is empty for an administrator and for "all stores"; store
// staff have no scope; the list is sorted and without repeats.
func (u *User) ValidateScope() error {
	if !u.Role.Known() {
		return fmt.Errorf("%w (%q)", ErrInvalidRole, u.Role)
	}
	if u.Role.StoreStaff() {
		u.AllStores, u.Stores = false, nil
		return nil
	}
	if u.Role == RoleAdmin {
		u.AllStores = true
	}
	if u.AllStores {
		u.Stores = nil
		return nil
	}
	if len(u.Stores) > maxScopeStores {
		return ErrInvalidStore
	}
	for _, s := range u.Stores {
		if !storePattern.MatchString(s) {
			return ErrInvalidStore
		}
	}
	u.Stores = slices.Compact(slices.Sorted(slices.Values(u.Stores)))
	if len(u.Stores) == 0 {
		return ErrNoStores
	}
	return nil
}

// ScopeName is the readable form of the stores the user sees.
func (u User) ScopeName() string {
	if u.SeesAllStores() {
		return "bütün mağazalar"
	}
	return strings.Join(u.Stores, ", ")
}

// SeesAllStores means seeing all the stores.
func (u User) SeesAllStores() bool { return u.Role == RoleAdmin || u.AllStores }

// SeesStore means being able to see the store.
func (u User) SeesStore(id string) bool { return u.SeesAllStores() || slices.Contains(u.Stores, id) }

// VisibleStores is the reports' filter: nil is all the stores.
func (u User) VisibleStores() []string {
	if u.SeesAllStores() {
		return nil
	}
	return append([]string{}, u.Stores...)
}

// CanManageUsers means being able to manage users at headquarters.
func (u User) CanManageUsers() bool { return u.Role == RoleAdmin }

// CanEditCatalog means being able to change products and headquarters prices.
func (u User) CanEditCatalog() bool { return u.Role == RoleAdmin || u.Role == RoleCatalog }
