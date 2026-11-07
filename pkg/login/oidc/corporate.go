// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel"
)

// People is the person record that links the corporate identity to the person (personnel.Manage).
type People interface {
	// Identify returns the person linked to the identity (see personnel.Manage.Identify).
	Identify(ctx context.Context, id personnel.Identity) (personnel.User, error)
	// Own is the signed-in person's own record.
	Own(ctx context.Context, self personnel.Actor) (personnel.User, error)
	LinkOwn(ctx context.Context, self personnel.Actor, issuer, subject string) (personnel.User, error)
	Unlink(ctx context.Context, actor personnel.Actor, id string) (personnel.User, error)
}

var _ People = (*personnel.Manage)(nil)

// Service is what the service that sets up the corporate sign-in provides.
type Service struct {
	People People
	// Open opens the service's session for a person whose identity is proven and who is tied
	// to a record. The service's rules are here (which roles can sign in, the
	// session's fingerprint); a closed account is rejected before it reaches Open.
	Open func(ctx context.Context, u personnel.User) (token string, p login.Person, err error)
	// Cookie is the session cookie.
	Cookie login.Cookie
	Log    *slog.Logger
}

// Corporate is a service's corporate sign-in setup. If it is not installed (nil) it leaves
// all operations to the service's own sign-in.
type Corporate struct {
	mode login.Mode
	flow *Flow
	svc  Service
}

// Configure sets up the corporate sign-in from the ABAROW_LOGIN and ABAROW_OIDC_* settings.
// It returns nil if the corporate sign-in is not on. If the setting is invalid it returns an error:
// a wrongly set up sign-in must not stay silently off.
func Configure(getenv func(string) string, svc Service, opts ...Option) (*Corporate, error) {
	mode, err := login.ParseMode(getenv("ABAROW_LOGIN"))
	if err != nil {
		return nil, err
	}
	cfg, set, err := LoadConfig(getenv)
	if err != nil {
		return nil, err
	}
	if svc.Log == nil {
		svc.Log = slog.Default()
	}
	switch {
	case !mode.OIDC && set:
		svc.Log.Warn("ABAROW_OIDC_* is set but ABAROW_LOGIN does not include oidc; corporate sign-in is off")
		return nil, nil
	case !mode.OIDC:
		return nil, nil
	case !set:
		return nil, fmt.Errorf("ABAROW_LOGIN oidc içeriyor ama ABAROW_OIDC_ISSUER yok")
	}
	c := &Corporate{mode: mode, svc: svc}
	opts = append([]Option{WithLogger(svc.Log)}, opts...)
	if c.flow, err = New(cfg, c.signIn, svc.Cookie, opts...); err != nil {
		return nil, err
	}
	if !mode.Local {
		svc.Log.Warn("only the corporate sign-in is on; if the IdP cannot be reached the screen cannot be signed in to (sales are not affected)")
	}
	return c, nil
}

// Wrap adds the corporate sign-in to the service's own sign-in adapter.
func (c *Corporate) Wrap(local login.Authenticator) login.Authenticator {
	if c == nil {
		return local
	}
	return login.WithMethods(local, c.mode.Local, c.flow.Method())
}

// Handle adds the corporate sign-in and identity linking endpoints to the mux. web
// must have been built with the adapter Wrap returned.
func (c *Corporate) Handle(mux *http.ServeMux, web login.Web) {
	if c == nil {
		return
	}
	c.flow.Handle(mux)
	c.flow.HandleAccount(mux, web, account{c.svc.People})
}

func (c *Corporate) signIn(ctx context.Context, cl Claims) (string, login.Person, error) {
	u, err := c.svc.People.Identify(ctx, personnel.Identity{
		Issuer: cl.Issuer, Subject: cl.Subject, Email: cl.Email, EmailVerified: cl.EmailVerified,
	})
	switch {
	case errors.Is(err, personnel.ErrNoIdentity):
		return "", login.Person{}, login.Refused("bu kurumsal kimliğe bağlı bir hesap yok")
	case err != nil:
		return "", login.Person{}, err
	case u.Disabled:
		return "", login.Person{}, login.Refused("hesap kapalı")
	}
	return c.svc.Open(ctx, u)
}

// account is an Account over the person record.
type account struct{ people People }

func actorOf(p login.Person) personnel.Actor { return personnel.Actor{ID: p.Username, Name: p.Name} }

func (a account) Link(ctx context.Context, p login.Person, c Claims) error {
	_, err := a.people.LinkOwn(ctx, actorOf(p), c.Issuer, c.Subject)
	if errors.Is(err, personnel.ErrIdentityTaken) {
		return ErrTaken
	}
	return err
}

func (a account) Unlink(ctx context.Context, p login.Person) error {
	_, err := a.people.Unlink(ctx, actorOf(p), p.Username)
	return err
}

func (a account) Linked(ctx context.Context, p login.Person) (bool, error) {
	u, err := a.people.Own(ctx, actorOf(p))
	return u.Subject != "", err
}
