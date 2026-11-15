// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"mime"
	"net/http"
	"strings"
	"time"
)

// Cookie is the session cookie. The cookie cannot be read from JavaScript (HttpOnly) and
// is not sent with requests from other sites (SameSite=Strict). If the screen is
// served over HTTPS (directly or behind a reverse proxy with TLS) the cookie is
// sent only over HTTPS (Secure); on plain HTTP on the store network
// the mark is not set.
type Cookie struct {
	// Name is the cookie's name (abarow_report, abarow_headquarters).
	Name string
	// TTL is the cookie's lifetime; it must be the same as the session's duration.
	TTL time.Duration
}

// Token is the session key in the request's cookie; empty if none.
func (c Cookie) Token(r *http.Request) string {
	if ck, err := r.Cookie(c.Name); err == nil {
		return ck.Value
	}
	return ""
}

// Set writes the session key to the cookie.
func (c Cookie) Set(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: c.Name, Value: token, Path: "/", MaxAge: int(c.TTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: OverHTTPS(r),
	})
}

// Clear deletes the cookie.
func (c Cookie) Clear(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: c.Name, Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: OverHTTPS(r),
	})
}

// OverHTTPS says the request came over HTTPS; a reverse proxy with TLS reports
// it with X-Forwarded-Proto. Even if the header is fake it does no harm:
// it only prevents the cookie from being sent over HTTP.
func OverHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// JSONRequest says the request has a JSON body. A form on another site cannot
// send JSON (the browser asks for permission first): sign-in, sign-out and write
// requests cannot be triggered by fake requests.
func JSONRequest(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}
