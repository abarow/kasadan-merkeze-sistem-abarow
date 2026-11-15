// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/services/store/internal/app"
)

// cookieName is the name of the session cookie (its flags are in pkg/login). Since the store
// server is served over HTTP on the store network, the cookie gets the Secure flag only
// on HTTPS (a reverse proxy with TLS).
const cookieName = "abarow_report"

// SessionCookie is the cookie of the screen session.
var SessionCookie = login.Cookie{Name: cookieName, TTL: app.SessionTTL}

var sessionCookie = SessionCookie

// newWeb sets up the sign-in endpoints and the session check (pkg/login.Web) with the store
// server's manager code + PIN and store password adapter; it adds the corporate
// sign-in (OIDC) from corp, and if corp is nil only these methods.
func newWeb(auth *app.Auth, corp *oidc.Corporate) login.Web {
	return login.Web{Auth: corp.Wrap(auth.Authenticator()), Cookie: sessionCookie}
}

// by is the name written to the record of the change the request makes: the manager's name or
// "store password".
func by(r *http.Request) string { return login.FromRequest(r).Name }
