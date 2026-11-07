// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/personnel"
	"abarow.com/services/store/internal/domain"
)

// Signing in to the store server screen is open in two ways:
//   - manager code and PIN: the managers with open accounts managed at the store
//     server (their PINs at the register). The manager's name is written to the change records.
//   - the store password (set at install; its bcrypt digest is stored): for the first
//     install and for a store with no manager defined. "store password" is written to the
//     records. While a manager with an open account exists it is off (decision
//     29.10.2026); the recovery path of the only manager who forgot their PIN is the command line
//     (report set-pin).
//
// Sales figures are a trade secret; the API asks for a sign-in.
const (
	// MinPasswordLen is the shortest length of the store password.
	MinPasswordLen = 8
	// SessionTTL is how long a session is valid.
	SessionTTL = 12 * time.Hour
)

// StorePasswordName is the name written to the change records of a session opened with
// the store password.
const StorePasswordName = "mağaza parolası"

var (
	// ErrWrongPassword means the password is wrong.
	ErrWrongPassword = login.Wrong("parola yanlış")
	// ErrPasswordTooShort means the new password is too short.
	ErrPasswordTooShort = fmt.Errorf("parola en az %d karakter olmalı", MinPasswordLen)
	// ErrWrongPIN means the manager code or PIN is wrong.
	ErrWrongPIN = login.Wrong("kod ya da PIN yanlış")
	// ErrNotManager means the PIN is right but the person is not a manager.
	ErrNotManager = login.Refused("operasyona yalnızca müdürler girebilir")
	// ErrAccountClosed means the PIN is right but the account is closed.
	ErrAccountClosed = login.Refused("hesabınız kapalı; başka bir müdüre başvurun")
	// ErrPasswordClosed is a sign-in with the store password while a manager with
	// an open account exists.
	ErrPasswordClosed = login.Refused("mağaza parolası kapalı: operasyonda hesabı açık müdür var; müdür kodunuz ve PIN'inizle girin")
	// ErrNoManagerLogin is a manager sign-in while the store server does not manage cashiers
	// (no store id).
	ErrNoManagerLogin = login.Refused("müdür girişi kapalı: operasyonun mağaza kimliği tanımlı değil; mağaza parolasıyla girin")
	// ErrNoSession means there is no session, or it has expired or been closed.
	ErrNoSession = login.ErrNoSession
)

// LockedError is returned while the sign-in is locked after many wrong attempts.
type LockedError = login.LockedError

// HashPassword returns the digest (bcrypt) of the store password to be stored.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// ManagerDirectory is where the person is found at a manager sign-in (the store
// server's cashiers).
type ManagerDirectory interface {
	// Cashier returns the cashier; domain.ErrCashierNotFound if none.
	Cashier(ctx context.Context, id string) (domain.Cashier, error)
	Cashiers(ctx context.Context) ([]domain.Cashier, error)
}

// PeopleDirectory makes the person store the directory of the manager sign-in (cashiers
// are pkg/personnel's single person model).
type PeopleDirectory struct{ personnel.Store }

// Cashier returns the cashier; domain.ErrCashierNotFound if none.
func (d PeopleDirectory) Cashier(ctx context.Context, id string) (domain.Cashier, error) {
	return d.User(ctx, id)
}

// Cashiers returns all the cashiers (including closed accounts).
func (d PeopleDirectory) Cashiers(ctx context.Context) ([]domain.Cashier, error) {
	return d.Users(ctx)
}

// Who is the person who opened the session. In a session opened with the store password CashierID
// is empty and Name is StorePasswordName.
type Who struct {
	CashierID string
	Name      string
}

// storeSubject is the person of a session opened with the store password (manager codes
// cannot be empty, so there is no collision).
const storeSubject = ""

// Auth manages the screen sessions. The session, the lock and the person lookup belong to the
// pkg/login core; here there are two adapters: the store password and the manager code + PIN.
// Sessions are kept in memory; after the service restarts people sign in again.
// Only the digest of the session key is stored.
type Auth struct {
	hash     []byte
	now      func() time.Time
	managers ManagerDirectory // nil: the manager sign-in is off
	guard    *login.Guard
	sessions *login.Sessions
}

// NewAuth returns an Auth with the bcrypt digest of the store password (HashPassword).
// Sessions are kept in sessions (PostgreSQL; login.NewMemory in tests).
func NewAuth(hash string, sessions login.Store) (*Auth, error) {
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return nil, fmt.Errorf("parola özeti geçersiz (bcrypt olmalı): %w", err)
	}
	a := &Auth{hash: []byte(hash), now: time.Now}
	clock := func() time.Time { return a.now() }
	a.guard = login.NewGuard(login.Default, clock)
	a.sessions = login.NewSessions(sessions, a, SessionTTL, clock)
	return a, nil
}

// AllowManagers turns on managers signing in with their own code and PIN.
func (a *Auth) AllowManagers(d ManagerDirectory) { a.managers = d }

// ManagerLogin says that the manager sign-in can be used: the store server manages
// cashiers and there is at least one manager with an open account.
func (a *Auth) ManagerLogin(ctx context.Context) (bool, error) {
	if a.managers == nil {
		return false, nil
	}
	list, err := a.managers.Cashiers(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(list, activeManager), nil
}

func activeManager(c domain.Cashier) bool { return c.Role == domain.RoleManager && !c.Disabled }

// Login returns a new session key if the store password is right. While a manager with an
// open account exists the password is not compared (ErrPasswordClosed); a wrong
// attempt is not counted either.
func (a *Auth) Login(ctx context.Context, password string) (string, error) {
	switch closed, err := a.ManagerLogin(ctx); {
	case err != nil:
		return "", err
	case closed:
		return "", ErrPasswordClosed
	}
	ok, err := a.guard.Attempt("parola", func() bool {
		return bcrypt.CompareHashAndPassword(a.hash, []byte(password)) == nil
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrWrongPassword
	}
	return a.sessions.Open(ctx, storeSubject, nil)
}

// LoginManager returns a new session key and the manager if the manager's code and PIN are
// right. An unknown code is answered like a wrong PIN.
func (a *Auth) LoginManager(ctx context.Context, id, pin string) (string, Who, error) {
	if a.managers == nil {
		return "", Who{}, ErrNoManagerLogin
	}
	if domain.ValidateCashierID(id) != nil {
		return "", Who{}, ErrWrongPIN
	}
	c, err := a.managers.Cashier(ctx, id)
	if errors.Is(err, domain.ErrCashierNotFound) {
		return "", Who{}, ErrWrongPIN
	}
	if err != nil {
		return "", Who{}, err
	}
	ok, err := a.guard.Attempt("pin:"+id, func() bool {
		return bcrypt.CompareHashAndPassword(c.PINHash, []byte(pin)) == nil
	})
	if err != nil {
		return "", Who{}, err
	}
	switch {
	case !ok:
		return "", Who{}, ErrWrongPIN
	case c.Disabled:
		return "", Who{}, ErrAccountClosed
	case c.Role != domain.RoleManager:
		return "", Who{}, ErrNotManager
	}
	// The session's fingerprint is the PIN digest: when the PIN changes the sessions opened with
	// that PIN close.
	token, err := a.sessions.Open(ctx, c.ID, c.PINHash)
	return token, Who{CashierID: c.ID, Name: c.Name}, err
}

// Resolve reads the session's person from the live record (login.Directory).
// A manager's session is checked on every request: if the account is closed, the managership is
// removed or the PIN changes the session closes (ErrNoSession). If the name changed it returns
// the new name. A session opened with the store password closes when an account-open manager
// exists (e.g. the first manager was added).
func (a *Auth) Resolve(ctx context.Context, s login.Session) (login.Person, error) {
	if s.Subject == storeSubject {
		switch closed, err := a.ManagerLogin(ctx); {
		case err != nil:
			return login.Person{}, err
		case closed:
			return login.Person{}, ErrNoSession
		}
		return personOf(Who{Name: StorePasswordName}), nil
	}
	if a.managers == nil {
		return login.Person{}, ErrNoSession
	}
	c, err := a.managers.Cashier(ctx, s.Subject)
	switch {
	case errors.Is(err, domain.ErrCashierNotFound):
		return login.Person{}, ErrNoSession
	case err != nil:
		return login.Person{}, err
	case activeManager(c) && bytes.Equal(c.PINHash, s.Binding):
		return personOf(Who{CashierID: c.ID, Name: c.Name}), nil
	}
	return login.Person{}, ErrNoSession
}

// Session returns the person who opened the session; ErrNoSession if the session is invalid.
func (a *Auth) Session(ctx context.Context, token string) (Who, error) {
	p, err := a.sessions.Person(ctx, token)
	if err != nil {
		return Who{}, err
	}
	return Who{CashierID: p.Username, Name: p.Name}, nil
}

// KeepAfterPINChange keeps this session open when a manager changes their own PIN; the
// other sessions opened with that PIN close.
func (a *Auth) KeepAfterPINChange(token string, c domain.Cashier) {
	_ = a.sessions.Rebind(context.Background(), token, c.ID, c.PINHash) // memory: it returns no error
}

// Logout closes the session.
func (a *Auth) Logout(token string) {
	_ = a.sessions.Close(context.Background(), token) // memory: it returns no error
}

// OIDCService is the person record and the way of opening a session that the corporate
// sign-in (pkg/login/oidc) uses in this service: the IdP proves the identity, the managership is read
// from the person record. The session is tied to the PIN digest like in a manager code sign-in
// (it closes when the PIN changes).
func (a *Auth) OIDCService(people oidc.People, cookie login.Cookie, log *slog.Logger) oidc.Service {
	return oidc.Service{
		People: people, Cookie: cookie, Log: log,
		Open: func(ctx context.Context, u personnel.User) (string, login.Person, error) {
			if !activeManager(u) {
				return "", login.Person{}, ErrNotManager
			}
			token, err := a.sessions.Open(ctx, u.ID, u.PINHash)
			return token, personOf(Who{CashierID: u.ID, Name: u.Name}), err
		},
	}
}

// Authenticator is Auth's adapter that connects to the sign-in screen (pkg/login.Web):
// the manager code and PIN or the store password. While a manager with an open account exists only the
// manager sign-in is offered (the store password is off).
func (a *Auth) Authenticator() login.Authenticator { return managerLogin{a} }

type managerLogin struct{ a *Auth }

func (l managerLogin) Methods(ctx context.Context) (login.Methods, error) {
	ok, err := l.a.ManagerLogin(ctx)
	if err != nil {
		return login.Methods{}, err
	}
	if ok {
		return login.Methods{Methods: []login.Method{{
			ID: "manager", Kind: login.KindForm, Label: "Müdür PIN'iyle gir",
			Title: "Müdür girişi", Lead: "Kasadaki müdür kodunuz ve PIN'inizle girin.",
			Fields: []login.Field{
				{Name: "cashier_id", Label: "Müdür kodu", Type: "text"},
				{Name: "pin", Label: "PIN", Type: "password", Numeric: true},
			},
			Note: "Müdür tanımlı olduğu için mağaza parolası kapalı. PIN'ini unutan müdüre başka bir müdür Kasiyerler " +
				"bölümünden yeni PIN verir; başka müdür yoksa operasyonda store-install.sh pin <kod> komutuyla.",
			NoteID: "password-closed",
		}}}, nil
	}
	return login.Methods{Methods: []login.Method{{
		ID: "password", Kind: login.KindForm, Label: "Mağaza parolasıyla gir",
		Title: "Mağaza parolasıyla giriş", Lead: "Raporları görmek için mağaza parolasını girin.",
		Fields: []login.Field{{Name: "password", Label: "Parola", Type: "password"}},
		Note: "Kasiyerler bölümünde müdür tanımlanınca müdürler kendi kodu ve PIN'iyle girer; değişiklik kayıtlarında " +
			"adları görünür. Mağaza parolası o zaman kapanır.",
	}}}, nil
}

func (l managerLogin) Login(ctx context.Context, fields map[string]string) (string, login.Person, error) {
	if id := fields["cashier_id"]; id != "" {
		token, who, err := l.a.LoginManager(ctx, id, fields["pin"])
		if err != nil {
			return "", login.Person{}, err
		}
		return token, personOf(who), nil
	}
	token, err := l.a.Login(ctx, fields["password"])
	if err != nil {
		return "", login.Person{}, err
	}
	return token, personOf(Who{Name: StorePasswordName}), nil
}

func (l managerLogin) Session(ctx context.Context, token string) (login.Person, error) {
	return l.a.sessions.Person(ctx, token)
}

func (l managerLogin) Logout(ctx context.Context, token string) error {
	return l.a.sessions.Close(ctx, token)
}

// personOf is the form in pkg/login of whoever opened the session: everyone who signs in to the
// store server is the store's manager (with the store password the code is empty).
func personOf(w Who) login.Person {
	return login.Person{Username: w.CashierID, Name: w.Name, Role: "manager", AllStores: true}
}
