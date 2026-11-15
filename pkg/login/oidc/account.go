// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"net/http"

	"abarow.com/pkg/httpjson"
	"abarow.com/pkg/login"
)

// ErrTaken means the corporate identity to be linked is linked to another person.
var ErrTaken = errors.New("bu kurumsal kimlik başka bir kişiye bağlı")

// Account is the signed-in person's own corporate identity link. The person themselves
// proves the identity at the IdP; no one can link on behalf of someone else.
type Account interface {
	// Link links the identity to the person's account; ErrTaken if it is linked to another person.
	Link(ctx context.Context, p login.Person, c Claims) error
	// Unlink removes the person's identity link.
	Unlink(ctx context.Context, p login.Person) error
	// Linked says the person is linked to an identity.
	Linked(ctx context.Context, p login.Person) (bool, error)
}

// LinkPath is the endpoint that redirects the signed-in person to the IdP and links their
// identity to their account; UnlinkPath removes the link; AccountPath gives the link's state.
const (
	LinkPath    = "/api/auth/oidc/link"
	UnlinkPath  = "/api/auth/oidc/unlink"
	AccountPath = "/api/auth/oidc"
)

// AccountJSON is the person's corporate identity state (GET AccountPath).
type AccountJSON struct {
	Linked bool `json:"linked"`
}

// HandleAccount adds the identity linking endpoints to the mux. Linking is a GET: the screen
// opens it with a link; the SameSite=Strict session cookie prevents another site from
// triggering it.
func (f *Flow) HandleAccount(mux *http.ServeMux, web login.Web, account Account) {
	f.account = account
	mux.HandleFunc("GET "+LinkPath, web.Require(func(w http.ResponseWriter, r *http.Request) {
		p := login.FromRequest(r)
		if p.Username == "" { // a session not tied to a person (store password)
			httpjson.Error(w, http.StatusForbidden, "kurumsal kimlik, kişisel hesapla girenin hesabına bağlanır")
			return
		}
		f.begin(w, r, &p)
	}))
	mux.HandleFunc("POST "+UnlinkPath, web.Require(func(w http.ResponseWriter, r *http.Request) {
		p := login.FromRequest(r)
		if p.Username == "" {
			httpjson.Error(w, http.StatusForbidden, "kurumsal kimlik, kişisel hesapla girenin hesabından kaldırılır")
			return
		}
		if err := account.Unlink(r.Context(), p); err != nil {
			httpjson.Error(w, http.StatusServiceUnavailable, "kimlik bağı kaldırılamadı: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET "+AccountPath, web.Require(func(w http.ResponseWriter, r *http.Request) {
		p := login.FromRequest(r)
		linked := false
		if p.Username != "" {
			var err error
			if linked, err = account.Linked(r.Context(), p); err != nil {
				httpjson.Error(w, http.StatusServiceUnavailable, "kimlik durumu okunamadı: "+err.Error())
				return
			}
		}
		httpjson.Write(w, http.StatusOK, AccountJSON{Linked: linked})
	}))
}
