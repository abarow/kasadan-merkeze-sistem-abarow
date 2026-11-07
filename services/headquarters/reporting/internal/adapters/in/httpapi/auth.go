// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// cookieName is the name of the session cookie (its flags are in pkg/login).
const cookieName = "abarow_headquarters"

// SessionCookie is the cookie of the screen session.
var SessionCookie = login.Cookie{Name: cookieName, TTL: app.SessionTTL}

var sessionCookie = SessionCookie

// authHandler serves the sign-in endpoints (pkg/login.Web) and the user's own
// password.
type authHandler struct {
	auth *app.Auth
	login.Web
}

// newAuthHandler sets up the sign-in endpoints; it adds the corporate sign-in (OIDC) from corp,
// and if corp is nil only username and password.
func newAuthHandler(auth *app.Auth, corp *oidc.Corporate) *authHandler {
	return &authHandler{auth: auth, Web: login.Web{Auth: corp.Wrap(auth.Authenticator()), Cookie: sessionCookie}}
}

// password changes the person's own password (with their current password).
// All of the person's sessions close; this browser is given a new session
// cookie.
func (h *authHandler) password(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := h.auth.ChangeOwnPassword(r.Context(), me(r), body.Current, body.New)
	var locked app.LockedError
	switch {
	case errors.As(err, &locked), errors.Is(err, app.ErrWrongCurrentPassword):
		writeError(w, login.Status(err), err.Error())
		return
	case errors.Is(err, domain.ErrPasswordTooShort):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "parola değiştirilemedi: "+err.Error())
		return
	}
	sessionCookie.Set(w, r, token)
	w.WriteHeader(http.StatusNoContent)
}

// me is the user that require puts on the request.
func me(r *http.Request) domain.User { return app.UserOf(login.FromRequest(r)) }
