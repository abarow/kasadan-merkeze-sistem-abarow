// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/login/oidc/oidctest"
	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/pkg/report"
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

const testClient = "abarow-headquarters"

// corporate is a headquarters set up with the corporate sign-in.
func corporate(t *testing.T, login string) (http.Handler, *oidctest.IdP) {
	t.Helper()
	idp := oidctest.New(t, testClient)
	auth, err := app.NewAuth(personneltest.New())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := auth.AddUser(ctx, app.Installer, "ayse", "headquarters-test-1", app.UserInput{Name: "Ayşe Yılmaz", Role: domain.RoleAdmin, Email: "ayse@firma.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AddUser(ctx, app.Installer, "ali", "headquarters-test-2", app.UserInput{Name: "Ali Kaya", Role: domain.RoleReport, Stores: []string{"store-1"}, Email: "ali@firma.example"}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"ABAROW_LOGIN": login, "ABAROW_OIDC_ISSUER": idp.URL(), "ABAROW_OIDC_CLIENT_ID": testClient,
		"ABAROW_OIDC_REDIRECT_URL": "http://localhost:8080" + oidc.CallbackPath,
	}
	corp, err := oidc.Configure(func(k string) string { return env[k] }, auth.OIDCService(SessionCookie, nil))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>headquarters"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(app.NewService(reports{}, loc), report.New(reports{}, loc), auth, corp, web, nil, nil), idp
}

// browser is a browser that keeps cookies.
type browser struct {
	t   *testing.T
	h   http.Handler
	jar map[string]string
}

func (b *browser) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	return rec
}

// viaIdP starts from the first endpoint, confirms the identity given at the IdP and returns.
func (b *browser) viaIdP(idp *oidctest.IdP, first, sub, email string) string {
	b.t.Helper()
	idp.Sub, idp.Email = sub, email
	rec := b.do(http.MethodGet, first, "")
	if rec.Code != http.StatusSeeOther {
		b.t.Fatalf("%s: %d %s", first, rec.Code, rec.Body)
	}
	cb, _ := idp.Authorize(b.t, rec.Header().Get("Location"))
	u, _ := url.Parse(cb)
	rec = b.do(http.MethodGet, u.RequestURI(), "")
	return rec.Header().Get("Location")
}

func TestCorporateLogin(t *testing.T) {
	h, idp := corporate(t, "yerel,oidc")
	b := &browser{t: t, h: h, jar: map[string]string{}}

	rec := b.do(http.MethodGet, "/api/auth/methods", "")
	if !strings.Contains(rec.Body.String(), `"id":"password"`) || !strings.Contains(rec.Body.String(), `"id":"oidc","kind":"redirect"`) {
		t.Errorf("methods: %s", rec.Body)
	}

	// The first sign-in is matched by email; the role and store permission come from
	// the user record, not from the IdP.
	ali := &browser{t: t, h: h, jar: map[string]string{}}
	if got := ali.viaIdP(idp, oidc.StartPath, "s-ali", "ali@firma.example"); got != "/" {
		t.Fatalf("ali: %q", got)
	}
	rec = ali.do(http.MethodGet, "/api/auth/session", "")
	if !strings.Contains(rec.Body.String(), `"username":"ali"`) || !strings.Contains(rec.Body.String(), `"role":"report"`) ||
		!strings.Contains(rec.Body.String(), `"stores":["store-1"]`) || !strings.Contains(rec.Body.String(), `"all_stores":false`) {
		t.Errorf("oturum: %s", rec.Body)
	}
	if rec := ali.do(http.MethodGet, "/api/users", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a report user saw the users: %d", rec.Code)
	}
	if rec := ali.do(http.MethodGet, "/api/reports/summary?from=2026-10-01&to=2026-10-03", ""); rec.Code != http.StatusOK {
		t.Errorf("rapor: %d", rec.Code)
	}

	// The administrator sees the user: the address and the link state; removes the identity link.
	if rec := b.do(http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"headquarters-test-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("administrator sign-in: %d", rec.Code)
	}
	var got struct {
		User userJSON `json:"user"`
	}
	rec = b.do(http.MethodGet, "/api/users/ali", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.User.Email != "ali@firma.example" || !got.User.Linked {
		t.Fatalf("user: %s %v", rec.Body, err)
	}
	if rec := b.do(http.MethodPost, "/api/users/ali/identity/unlink", "{}"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Errorf("link removal: %d %s", rec.Code, rec.Body)
	}
	if rec := ali.do(http.MethodPost, "/api/users/ayse/identity/unlink", "{}"); rec.Code != http.StatusForbidden {
		t.Errorf("a report user removed someone else's link: %d", rec.Code)
	}
	// When the link is removed the same person is matched by address again (recovery from losing the identity).
	ali2 := &browser{t: t, h: h, jar: map[string]string{}}
	if got := ali2.viaIdP(idp, oidc.StartPath, "s-ali-yeni", "ali@firma.example"); got != "/" {
		t.Errorf("rematch: %q", got)
	}

	// The address is given from the user screen; two users cannot have the same address.
	if rec := b.do(http.MethodPost, "/api/users/ali", `{"name":"Ali Kaya","role":"report","stores":["store-1"],"email":"AYSE@firma.example"}`); rec.Code != http.StatusConflict {
		t.Errorf("same address: %d %s", rec.Code, rec.Body)
	}
	if rec := b.do(http.MethodPost, "/api/users/ali", `{"name":"Ali Kaya","role":"report","stores":["store-1"],"email":"ali"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid address: %d %s", rec.Code, rec.Body)
	}

	// The person links an identity to their own account and removes the link themselves.
	ayse := &browser{t: t, h: h, jar: b.jar}
	var acct oidc.AccountJSON
	rec = ayse.do(http.MethodGet, oidc.AccountPath, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &acct); err != nil || acct.Linked {
		t.Fatalf("hesap: %s %v", rec.Body, err)
	}
	if got := ayse.viaIdP(idp, oidc.LinkPath, "s-ayse", "baska@firma.example"); got != "/?"+oidc.LinkedParam+"=1" {
		t.Fatalf("linking: %q", got)
	}
	rec = ayse.do(http.MethodGet, oidc.AccountPath, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &acct); err != nil || !acct.Linked {
		t.Errorf("after linking: %s %v", rec.Body, err)
	}
	if rec := ayse.do(http.MethodPost, oidc.UnlinkPath, "{}"); rec.Code != http.StatusNoContent {
		t.Errorf("removing own link: %d", rec.Code)
	}
}

func TestCorporateOnly(t *testing.T) {
	h, idp := corporate(t, "oidc")
	b := &browser{t: t, h: h, jar: map[string]string{}}
	if rec := b.do(http.MethodGet, "/api/auth/methods", ""); strings.Contains(rec.Body.String(), `"password"`) || !strings.Contains(rec.Body.String(), `"oidc"`) {
		t.Errorf("methods: %s", rec.Body)
	}
	// The password sign-in endpoint is off too.
	if rec := b.do(http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"headquarters-test-1"}`); rec.Code != http.StatusForbidden || len(b.jar) != 0 {
		t.Errorf("sign-in with a password: %d %v", rec.Code, b.jar)
	}
	if got := b.viaIdP(idp, oidc.StartPath, "s-ayse", "ayse@firma.example"); got != "/" {
		t.Fatalf("corporate sign-in: %q", got)
	}
	if rec := b.do(http.MethodGet, "/api/users", ""); rec.Code != http.StatusOK {
		t.Errorf("the administrator could not see the users: %d", rec.Code)
	}
}

// If the corporate sign-in is not installed the endpoints do not exist and the screen offers only the password.
func TestNoCorporate(t *testing.T) {
	h := newHandler(t)
	if rec := do(h, http.MethodGet, oidc.StartPath, "", nil); rec.Code == http.StatusSeeOther {
		t.Errorf("a corporate sign-in that is not installed redirected")
	}
	if rec := do(h, http.MethodGet, "/api/auth/methods", "", nil); strings.Contains(rec.Body.String(), "oidc") {
		t.Errorf("methods: %s", rec.Body)
	}
}
