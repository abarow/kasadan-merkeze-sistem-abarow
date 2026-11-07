// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"context"
	"fmt"
	"strings"
)

// The sign-in screen learns from the server which routes are open and draws the forms
// accordingly (ui/packages/admin, Giris.svelte). So the screen does not know a
// method (password, PIN, corporate sign-in) separately: the service lists whichever
// adapters it has connected.

const (
	// KindForm is a method whose fields are filled in and sent to the service.
	KindForm = "form"
	// KindRedirect is a method in which the browser is redirected to a URL (OIDC).
	KindRedirect = "redirect"
)

// Field is a field of the sign-in form. Its key in the request body is Name.
type Field struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Type: text or password.
	Type string `json:"type"`
	// Numeric asks for a numeric keyboard (PIN).
	Numeric bool `json:"numeric,omitempty"`
}

// Method is a route on the sign-in screen.
type Method struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Label is the text in the link to switch to the other methods and in the
	// redirect button.
	Label string `json:"label"`
	// Title is the form's accessible name; Lead is the explanation above the form.
	Title string `json:"title,omitempty"`
	Lead  string `json:"lead,omitempty"`
	// Fields are the form method's fields.
	Fields []Field `json:"fields,omitempty"`
	// URL is the redirect method's address.
	URL string `json:"url,omitempty"`
	// Note is the explanation shown under the form (e.g. why another route is
	// closed); NoteID is a marker for tests to find it.
	Note   string `json:"note,omitempty"`
	NoteID string `json:"note_id,omitempty"`
}

// Methods is the sign-in screen's response (GET /api/auth/methods).
type Methods struct {
	Methods []Method `json:"methods"`
}

// Mode is which sign-in routes are open in the service (ABAROW_LOGIN).
type Mode struct {
	// Local is the service's own methods (password, PIN, store password).
	Local bool
	// OIDC is the corporate sign-in (OpenID Connect).
	OIDC bool
}

// ParseMode resolves the ABAROW_LOGIN value: empty or "local" only the service's own
// methods, "oidc" only the corporate sign-in, "local,oidc" both together.
func ParseMode(s string) (Mode, error) {
	var m Mode
	for _, part := range strings.Split(s, ",") {
		switch strings.TrimSpace(part) {
		case "yerel":
			m.Local = true
		case "oidc":
			m.OIDC = true
		case "":
		default:
			return Mode{}, fmt.Errorf("ABAROW_LOGIN geçersiz: %q (yerel, oidc ya da yerel,oidc)", s)
		}
	}
	if !m.Local && !m.OIDC {
		m.Local = true
	}
	return m, nil
}

// WithMethods adds methods to the service's own sign-in adapter. If local is
// false the service's own methods both leave the list and are rejected at sign-in:
// hiding them from the screen is not enough, the endpoint must be closed too.
func WithMethods(a Authenticator, local bool, extra ...Method) Authenticator {
	return methods{Authenticator: a, local: local, extra: extra}
}

type methods struct {
	Authenticator
	local bool
	extra []Method
}

func (m methods) Methods(ctx context.Context) (Methods, error) {
	var out Methods
	if m.local {
		var err error
		if out, err = m.Authenticator.Methods(ctx); err != nil {
			return Methods{}, err
		}
	}
	out.Methods = append(out.Methods, m.extra...)
	return out, nil
}

func (m methods) Login(ctx context.Context, fields map[string]string) (string, Person, error) {
	if !m.local {
		return "", Person{}, Refused("bu servise yalnızca kurumsal girişle girilir")
	}
	return m.Authenticator.Login(ctx, fields)
}
