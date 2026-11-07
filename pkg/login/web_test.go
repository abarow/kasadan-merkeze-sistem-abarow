// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCookie(t *testing.T) {
	c := Cookie{Name: "abarow_test", TTL: 12 * time.Hour}

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	c.Set(w, r, "anahtar")
	ck := w.Result().Cookies()[0]
	if ck.Name != "abarow_test" || ck.Value != "anahtar" || ck.MaxAge != 12*3600 ||
		!ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Secure || ck.Path != "/" {
		t.Errorf("cookie: %+v", ck)
	}

	// HTTPS (reverse proxy or direct) makes the cookie Secure.
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	c.Set(w, r, "anahtar")
	if !w.Result().Cookies()[0].Secure {
		t.Error("the cookie is not Secure on HTTPS")
	}

	// The key in the request is read; if there is no cookie it returns empty.
	r = httptest.NewRequest("GET", "/", nil)
	if c.Token(r) != "" {
		t.Error("there is a key in a request with no cookie")
	}
	r.AddCookie(&http.Cookie{Name: "abarow_test", Value: "anahtar"})
	if c.Token(r) != "anahtar" {
		t.Error("the key could not be read")
	}

	w = httptest.NewRecorder()
	c.Clear(w, r)
	if ck := w.Result().Cookies()[0]; ck.MaxAge >= 0 || ck.Value != "" {
		t.Errorf("deleted cookie: %+v", ck)
	}
}

func TestJSONRequest(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/json":                  true,
		"application/json; charset=utf-8":   true,
		"text/plain":                        false,
		"application/x-www-form-urlencoded": false,
		"":                                  false,
	} {
		r := httptest.NewRequest("POST", "/", nil)
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		if got := JSONRequest(r); got != want {
			t.Errorf("%q: %v", ct, got)
		}
	}
}
