// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"abarow.com/pkg/httpjson"
)

// The sign-in endpoints and the session check are the same code on both screens (store server,
// headquarters); the services only connect the Authenticator adapter. The error → HTTP
// status mapping is also in one place here (Status).

var (
	// ErrWrong means the credential is wrong (401). Not saying in the message which
	// one is wrong is the adapter's job.
	ErrWrong = errors.New("kimlik bilgisi yanlış")
	// ErrRefused means what was asked cannot be done but the session is valid (403):
	// the account is closed, the sign-in route is closed, no authority, the current password is wrong.
	ErrRefused = errors.New("izin yok")
)

// rejection is an error that has a message shown to the user and falls into a class (ErrWrong,
// ErrRefused).
type rejection struct {
	msg  string
	kind error
}

func (r rejection) Error() string        { return r.msg }
func (r rejection) Is(target error) bool { return target == r.kind }

// Wrong returns an error in the ErrWrong class with the message msg (401).
func Wrong(msg string) error { return rejection{msg, ErrWrong} }

// Refused returns an error in the ErrRefused class with the message msg (403).
func Refused(msg string) error { return rejection{msg, ErrRefused} }

// Status is the HTTP status of a sign-in error: lock 429, wrong credential
// and no session 401, closed/unauthorized 403; the rest (like the database) 503.
func Status(err error) int {
	var locked LockedError
	switch {
	case errors.As(err, &locked):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrWrong), errors.Is(err, ErrNoSession):
		return http.StatusUnauthorized
	case errors.Is(err, ErrRefused):
		return http.StatusForbidden
	}
	return http.StatusServiceUnavailable
}

// Authenticator is the port of a service's sign-in adapters.
type Authenticator interface {
	// Methods are the sign-in routes that are open now.
	Methods(ctx context.Context) (Methods, error)
	// Login signs in with the fields in the form (Method.Fields); it returns the new
	// session's key and the person. If the error is in the ErrWrong, ErrRefused or LockedError
	// class it is shown to the user as it is.
	Login(ctx context.Context, fields map[string]string) (token string, p Person, err error)
	// Session returns the key's person; ErrNoSession if the session is invalid.
	Session(ctx context.Context, token string) (Person, error)
	// Logout closes the session.
	Logout(ctx context.Context, token string) error
}

// PersonJSON is the form of the person who opened the session that goes to the screen.
type PersonJSON struct {
	// Username is the username or cashier code; empty in a session not tied to a
	// person (the store server's store password).
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	// AllStores means seeing all the stores; if not, only Stores.
	AllStores bool     `json:"all_stores"`
	Stores    []string `json:"stores"`
}

// JSON is the person's form that goes to the screen.
func (p Person) JSON() PersonJSON {
	stores := p.Stores
	if stores == nil {
		stores = []string{}
	}
	return PersonJSON{Username: p.Username, Name: p.Name, Role: p.Role, AllStores: p.AllStores, Stores: stores}
}

// Web serves the sign-in endpoints (POST /api/auth/login, POST /api/auth/logout,
// GET /api/auth/session, GET /api/auth/methods) and the session check.
type Web struct {
	Auth   Authenticator
	Cookie Cookie
}

// Handle adds the sign-in endpoints to the mux.
func (w Web) Handle(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", w.login)
	mux.HandleFunc("POST /api/auth/logout", w.logout)
	mux.HandleFunc("GET /api/auth/session", w.session)
	mux.HandleFunc("GET /api/auth/methods", w.methods)
}

func (w Web) methods(rw http.ResponseWriter, r *http.Request) {
	m, err := w.Auth.Methods(r.Context())
	if err != nil {
		httpjson.Error(rw, http.StatusServiceUnavailable, err.Error())
		return
	}
	httpjson.Write(rw, http.StatusOK, m)
}

func (w Web) login(rw http.ResponseWriter, r *http.Request) {
	if !JSONRequest(r) {
		httpjson.Error(rw, http.StatusUnsupportedMediaType, "istek JSON olmalı")
		return
	}
	var fields map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4096)).Decode(&fields); err != nil {
		httpjson.Error(rw, http.StatusBadRequest, "istek okunamadı")
		return
	}
	token, p, err := w.Auth.Login(r.Context(), fields)
	if err != nil {
		code, msg := Status(err), err.Error()
		if code == http.StatusServiceUnavailable {
			msg = "giriş yapılamadı: " + msg
		}
		httpjson.Error(rw, code, msg)
		return
	}
	w.Cookie.Set(rw, r, token)
	httpjson.Write(rw, http.StatusOK, p.JSON())
}

func (w Web) logout(rw http.ResponseWriter, r *http.Request) {
	if !JSONRequest(r) {
		httpjson.Error(rw, http.StatusUnsupportedMediaType, "istek JSON olmalı")
		return
	}
	if err := w.Auth.Logout(r.Context(), w.Cookie.Token(r)); err != nil {
		httpjson.Error(rw, http.StatusServiceUnavailable, "çıkış yapılamadı: "+err.Error())
		return
	}
	w.Cookie.Clear(rw, r)
	rw.WriteHeader(http.StatusNoContent)
}

func (w Web) session(rw http.ResponseWriter, r *http.Request) {
	p, ok := w.person(rw, r)
	if !ok {
		return
	}
	httpjson.Write(rw, http.StatusOK, p.JSON())
}

// person is the person of the session in the cookie; it writes 401 if there is no session, 503
// if it cannot be checked.
func (w Web) person(rw http.ResponseWriter, r *http.Request) (Person, bool) {
	p, err := w.Auth.Session(r.Context(), w.Cookie.Token(r))
	switch {
	case errors.Is(err, ErrNoSession):
		httpjson.Error(rw, http.StatusUnauthorized, err.Error())
		return Person{}, false
	case err != nil:
		httpjson.Error(rw, http.StatusServiceUnavailable, "oturum denetlenemedi: "+err.Error())
		return Person{}, false
	}
	return p, true
}

type personKey struct{}

// FromRequest is the person Require put on the request.
func FromRequest(r *http.Request) Person {
	p, _ := r.Context().Value(personKey{}).(Person)
	return p
}

// Require rejects the request with 401 if the session is not open; if it is open it puts the
// person on the request (FromRequest). Write requests accept only JSON:
// a form on another site cannot trigger them.
func (w Web) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		p, ok := w.person(rw, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet && !JSONRequest(r) {
			httpjson.Error(rw, http.StatusUnsupportedMediaType, "istek JSON olmalı")
			return
		}
		next(rw, r.WithContext(context.WithValue(r.Context(), personKey{}, p)))
	}
}

// Forward checks the session and forwards the request to the service behind (inventory,
// catalog-admin): the person goes in headers, headers of the same name put by the
// client are deleted. In a session not tied to a person (empty username)
// the name stands in for the username: the service behind accepts no unnamed person.
func (w Web) Forward(next http.Handler) http.HandlerFunc {
	return w.Require(func(rw http.ResponseWriter, r *http.Request) {
		p := FromRequest(r)
		if p.Username == "" {
			p.Username = p.Name
		}
		p.SetHeaders(r.Header)
		next.ServeHTTP(rw, r)
	})
}
