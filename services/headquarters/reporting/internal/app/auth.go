// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/personnel"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// Sign-in to the headquarters screen: personal accounts (username and
// password; the bcrypt digest is stored). Sessions are kept in the database: they are not
// lost when the service restarts; when the password changes or the account is
// closed all of the person's sessions close.
const (
	// SessionTTL is how long a session is valid.
	SessionTTL = 12 * time.Hour
)

var (
	// ErrWrongPassword means the username or password is wrong (which one is wrong is
	// not said).
	ErrWrongPassword = login.Wrong("kullanıcı adı ya da parola yanlış")
	// ErrNoSession means there is no session, it has expired or the account has been closed.
	ErrNoSession = login.ErrNoSession
	// ErrWrongCurrentPassword means the current password the person entered when changing their own
	// password is wrong.
	ErrWrongCurrentPassword = login.Refused("şu anki parola yanlış")
)

// LockedError is returned while the sign-in is locked after many wrong attempts
// (5 minutes after 5 wrong attempts, per username; the rule is in pkg/login).
type LockedError = login.LockedError

// UserChange is the record of a user change.
type UserChange = personnel.Change

// Users is where the users and sessions are kept (personneldb): it is written in the same
// transaction as the person's change record; when the password changes or the account is
// closed the person's sessions are deleted in the same transaction.
type Users interface {
	personnel.Store
	login.Store
}

// Auth manages the users and sessions. The session, the lock and the person
// headers belong to the pkg/login core; here there is only the username and password
// adapter.
type Auth struct {
	users    Users
	people   *personnel.Manage
	now      func() time.Time
	guard    *login.Guard
	sessions *login.Sessions
	// dummy is a digest so that a bcrypt comparison is done even for a non-existent user:
	// the response time does not reveal whether the user exists.
	dummy []byte
}

// NewAuth returns an Auth.
func NewAuth(users Users) (*Auth, error) {
	dummy, err := bcrypt.GenerateFromPassword([]byte("abarow-olmayan-kullanici"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	a := &Auth{users: users, now: time.Now, dummy: dummy}
	clock := func() time.Time { return a.now() }
	a.people = personnel.NewManage(users, personnel.Config{
		Roles:  []personnel.Role{personnel.RoleAdmin, personnel.RoleCatalog, personnel.RoleReport},
		Admins: []personnel.Role{personnel.RoleAdmin},
		Now:    clock,
	})
	a.guard = login.NewGuard(login.Default, clock)
	a.sessions = login.NewSessions(users, a, SessionTTL, clock)
	return a, nil
}

// Resolve reads the session's user from the live record (login.Directory):
// a role and store change takes effect immediately; the session of a person whose
// account is closed or deleted closes.
func (a *Auth) Resolve(ctx context.Context, s login.Session) (login.Person, error) {
	u, err := a.users.User(ctx, s.Subject)
	if errors.Is(err, domain.ErrUserNotFound) || err == nil && u.Disabled {
		return login.Person{}, ErrNoSession
	}
	if err != nil {
		return login.Person{}, err
	}
	return personOf(u), nil
}

// personOf is the user's form in the session; the administrator sees all the stores.
func personOf(u domain.User) login.Person {
	return login.Person{Username: u.ID, Name: u.Name, Role: string(u.Role), AllStores: u.SeesAllStores(), Stores: u.Stores}
}

// Login returns a new session key if the username and password are right.
// After successive wrong attempts with the same username the sign-in is locked.
func (a *Auth) Login(ctx context.Context, username, password string) (string, domain.User, error) {
	name, err := domain.Username(username)
	if err != nil {
		return "", domain.User{}, ErrWrongPassword
	}
	u, err := a.users.User(ctx, name)
	if err != nil && !errors.Is(err, domain.ErrUserNotFound) {
		return "", domain.User{}, err
	}
	missing := err != nil
	hash := u.PasswordHash
	if missing || hash == "" { // a person without a password (with a PIN) cannot sign in with a password
		missing, hash = true, string(a.dummy)
	}
	// The comparison is made in every case: the response time does not reveal whether the account
	// exists or is closed.
	ok, err := a.guard.Attempt(name, func() bool {
		match := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
		return !missing && !u.Disabled && match
	})
	if err != nil {
		return "", domain.User{}, err
	}
	if !ok {
		return "", domain.User{}, ErrWrongPassword
	}
	token, err := a.sessions.Open(ctx, name, nil)
	return token, u, err
}

// ChangeOwnPassword changes the person's own password: their current password is
// verified (wrong attempts are counted together with the sign-in, it locks).
// All of the person's sessions close (e.g. one left open on another device); a new session is
// opened for this request and its key is returned.
func (a *Auth) ChangeOwnPassword(ctx context.Context, u domain.User, current, next string) (string, error) {
	if err := domain.ValidatePassword(next); err != nil {
		return "", err
	}
	cur, err := a.users.User(ctx, u.ID)
	if err != nil {
		return "", err
	}
	ok, err := a.guard.Attempt(u.ID, func() bool {
		return bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte(current)) == nil
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrWrongCurrentPassword
	}
	if _, err := a.people.ChangeOwnSecret(ctx, personnel.Actor{ID: u.ID, Name: u.Name, Manage: true}, next); err != nil {
		return "", err
	}
	return a.sessions.Open(ctx, u.ID, nil)
}

// Session returns the user of the session key; ErrNoSession if the session is
// invalid.
func (a *Auth) Session(ctx context.Context, token string) (domain.User, error) {
	p, err := a.sessions.Person(ctx, token)
	if err != nil {
		return domain.User{}, err
	}
	return UserOf(p), nil
}

// UserOf is the user form of the person in the session.
func UserOf(p login.Person) domain.User {
	return domain.User{ID: p.Username, Name: p.Name, Role: domain.Role(p.Role), AllStores: p.AllStores, Stores: p.Stores}
}

// Logout closes the session.
func (a *Auth) Logout(ctx context.Context, token string) error {
	return a.sessions.Close(ctx, token)
}

// OIDCService is the person record and the way of opening a session that the corporate
// sign-in (pkg/login/oidc) uses in this service: the IdP proves the identity, the role and
// store permission are read from the user record.
func (a *Auth) OIDCService(cookie login.Cookie, log *slog.Logger) oidc.Service {
	return oidc.Service{
		People: a.people, Cookie: cookie, Log: log,
		Open: func(ctx context.Context, u personnel.User) (string, login.Person, error) {
			token, err := a.sessions.Open(ctx, u.ID, nil)
			return token, personOf(u), err
		},
	}
}

// Authenticator is Auth's adapter that connects to the sign-in screen: username
// and password (pkg/login.Web).
func (a *Auth) Authenticator() login.Authenticator { return passwordLogin{a} }

type passwordLogin struct{ a *Auth }

func (passwordLogin) Methods(context.Context) (login.Methods, error) {
	return login.Methods{Methods: []login.Method{{
		ID: "password", Kind: login.KindForm, Label: "Kullanıcı adı ve parolayla gir",
		Title: "Giriş", Lead: "Mağazaların raporlarını görmek için hesabınızla girin.",
		Fields: []login.Field{
			{Name: "username", Label: "Kullanıcı adı", Type: "text"},
			{Name: "password", Label: "Parola", Type: "password"},
		},
	}}}, nil
}

func (l passwordLogin) Login(ctx context.Context, fields map[string]string) (string, login.Person, error) {
	token, u, err := l.a.Login(ctx, fields["username"], fields["password"])
	if err != nil {
		return "", login.Person{}, err
	}
	return token, personOf(u), nil
}

func (l passwordLogin) Session(ctx context.Context, token string) (login.Person, error) {
	return l.a.sessions.Person(ctx, token)
}

func (l passwordLogin) Logout(ctx context.Context, token string) error { return l.a.Logout(ctx, token) }
