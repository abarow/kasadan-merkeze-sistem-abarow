// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func signIn(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	rec := do(h, http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, username, password), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s sign-in: %d %s", username, rec.Code, rec.Body)
	}
	return rec.Result().Cookies()[0]
}

// The administrator adds a user from the screen, gives their role and stores; a report
// user sees only their own stores, cannot manage users.
func TestUsersAPI(t *testing.T) {
	h := newHandler(t)
	admin := signIn(t, h, "ayse", "headquarters-test-1")

	add := func(body string) *httptest.ResponseRecorder {
		return do(h, http.MethodPost, "/api/users", body, admin)
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"username":"ali","name":"Ali","role":"report","password":"kısa"}`, http.StatusUnprocessableEntity},
		{`{"username":"ali","name":"Ali","role":"report","password":"headquarters-test-2"}`, http.StatusUnprocessableEntity}, // no store
		{`{"username":"ali","name":"Ali","role":"patron","all_stores":true,"password":"headquarters-test-2"}`, http.StatusUnprocessableEntity},
		{`{"username":"ayse","name":"Ayşe","role":"report","all_stores":true,"password":"headquarters-test-2"}`, http.StatusConflict},
		{`bozuk`, http.StatusBadRequest},
	} {
		if rec := add(tc.body); rec.Code != tc.want {
			t.Errorf("%s: %d %s", tc.body, rec.Code, rec.Body)
		}
	}
	rec := add(`{"username":"ali","name":"Ali Kaya","role":"report","stores":["store-2"],"password":"headquarters-test-2"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"stores":["store-2"]`) || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("ekleme: %d %s", rec.Code, rec.Body)
	}
	// A non-JSON write request (a form on another site) is rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/users/ali/disabled", strings.NewReader(`{"disabled":true}`))
	req.Header.Set("Content-Type", "text/plain")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON olmayan istek: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/users/ayse/disabled", `{"disabled":true}`, admin); rec.Code != http.StatusConflict {
		t.Errorf("closing own account: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/users/yok", "", admin); rec.Code != http.StatusNotFound {
		t.Errorf("non-existent user: %d", rec.Code)
	}

	// Report user: the role and stores are in the session; only their own
	// stores; user management is off.
	ali := signIn(t, h, "ali", "headquarters-test-2")
	if rec := do(h, http.MethodGet, "/api/auth/session", "", ali); !strings.Contains(rec.Body.String(), `"role":"report","all_stores":false,"stores":["store-2"]`) {
		t.Errorf("session: %s", rec.Body)
	}
	var stores struct {
		Stores []storeJSON `json:"stores"`
	}
	if rec := do(h, http.MethodGet, "/api/stores", "", ali); json.Unmarshal(rec.Body.Bytes(), &stores) != nil || len(stores.Stores) != 0 {
		t.Errorf("saw another store: %s", rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/reports/summary?store=store-1", "", ali); rec.Code != http.StatusForbidden {
		t.Errorf("summary of a store without permission: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/reports/summary", "", ali); rec.Code != http.StatusOK {
		t.Errorf("summary of their own stores: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/reports/store-prices", "", ali); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "store-1") {
		t.Errorf("prices of another store: %s", rec.Body)
	}
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"username":"veli","name":"Veli","role":"admin","password":"headquarters-test-3"}`},
		{http.MethodPost, "/api/users/ayse/password", `{"password":"headquarters-test-3"}`},
	} {
		if rec := do(h, r.method, r.path, r.body, ali); rec.Code != http.StatusForbidden {
			t.Errorf("report user %s %s: %d", r.method, r.path, rec.Code)
		}
	}

	// The person changes their own password: their other session closes, this browser is given
	// a new cookie.
	other := signIn(t, h, "ali", "headquarters-test-2")
	if rec := do(h, http.MethodPost, "/api/auth/password", `{"current_password":"yanlış-parola","new_password":"headquarters-test-5"}`, ali); rec.Code != http.StatusForbidden {
		t.Errorf("wrong current password: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodPost, "/api/auth/password", `{"current_password":"headquarters-test-2","new_password":"headquarters-test-5"}`, ali)
	if rec.Code != http.StatusNoContent || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("own password: %d %s", rec.Code, rec.Body)
	}
	ali = rec.Result().Cookies()[0]
	if do(h, http.MethodGet, "/api/auth/session", "", ali).Code != http.StatusOK || do(h, http.MethodGet, "/api/auth/session", "", other).Code != http.StatusUnauthorized {
		t.Error("when the password changes: this session should stay open, the other closed")
	}

	// A store and role change takes effect immediately; the changes are in the record.
	if rec := do(h, http.MethodPost, "/api/users/ali", `{"name":"Ali Kaya","role":"catalog","stores":["store-1"]}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/stores", "", ali); json.Unmarshal(rec.Body.Bytes(), &stores) != nil || len(stores.Stores) != 1 {
		t.Errorf("the new store did not show: %s", rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/users/ali/password", `{"password":"headquarters-test-4"}`, admin); rec.Code != http.StatusNoContent {
		t.Errorf("password: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/users/ali", "", admin)
	var detail struct {
		User    userJSON         `json:"user"`
		History []userChangeJSON `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.User.Role != "catalog" || len(detail.History) != 4 ||
		detail.History[0].Detail != "parola değişti" || detail.History[1].By != "Ayşe Yılmaz" || strings.Contains(rec.Body.String(), "headquarters-test") {
		t.Errorf("detail: %s", rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/users/ali/disabled", `{"disabled":true}`, admin); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disabled":true`) {
		t.Errorf("hesap kapatma: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/stores", "", ali); rec.Code != http.StatusUnauthorized {
		t.Errorf("session of the closed account: %d", rec.Code)
	}
	rec = do(h, http.MethodGet, "/api/users", "", admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"ali"`) || !strings.Contains(rec.Body.String(), `"username":"ayse","name":"Ayşe Yılmaz","role":"admin","all_stores":true,"stores":[]`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
}
