// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/login/oidc/oidctest"
	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/services/store/internal/adapters/out/cashierproto"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

const testClient = "abarow-store"

// corporate is a store server set up with the corporate sign-in: managers ali and veli,
// cashier zeynep.
func corporate(t *testing.T, login string) (h http.Handler, idp *oidctest.IdP, cashiers *app.Cashiers) {
	t.Helper()
	idp = oidctest.New(t, testClient)
	st := personneltest.New()
	n := 0
	cashiers = app.NewCashiers(st, cashierproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }})
	cashiers.HashPIN = func(pin string) ([]byte, error) { return bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost) }
	hash, _ := app.HashPassword("store-parolasi-1")
	auth, err := app.NewAuth(hash, st)
	if err != nil {
		t.Fatal(err)
	}
	auth.AllowManagers(app.PeopleDirectory{Store: st})
	ctx := context.Background()
	for _, c := range []struct {
		id, name string
		role     domain.Role
		pin      string
		email    string
	}{
		{"ali", "Ali Kaya", domain.RoleManager, "1234", "ali@firma.example"},
		{"veli", "Veli Er", domain.RoleManager, "5678", ""},
		{"zeynep", "Zeynep Demir", domain.RoleCashier, "2468", "zeynep@firma.example"},
	} {
		if _, err := cashiers.Add(ctx, "kurulum", c.id, c.name, c.role, c.pin, c.email); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{
		"ABAROW_LOGIN": login, "ABAROW_OIDC_ISSUER": idp.URL(), "ABAROW_OIDC_CLIENT_ID": testClient,
		"ABAROW_OIDC_REDIRECT_URL": "http://localhost:8080" + oidc.CallbackPath,
	}
	corp, err := oidc.Configure(func(k string) string { return env[k] }, auth.OIDCService(cashiers.People(), SessionCookie, nil))
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)
	return New(svc, auth, corp, t.TempDir(), nil, cashiers, nil), idp, cashiers
}

// browser is a browser that keeps cookies.
type browser struct {
	t   *testing.T
	h   http.Handler
	jar map[string]string
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	return &browser{t: t, h: h, jar: map[string]string{}}
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
	return b.do(http.MethodGet, u.RequestURI(), "").Header().Get("Location")
}

func TestCorporateLogin(t *testing.T) {
	h, idp, cashiers := corporate(t, "yerel,oidc")
	ctx := context.Background()
	anon := newBrowser(t, h)
	if rec := anon.do(http.MethodGet, "/api/auth/methods", ""); !strings.Contains(rec.Body.String(), `"id":"manager"`) ||
		!strings.Contains(rec.Body.String(), `"id":"oidc","kind":"redirect"`) {
		t.Errorf("methods: %s", rec.Body)
	}

	// A manager is recognized at the first sign-in by their email; the managership comes from the record.
	ali := newBrowser(t, h)
	if got := ali.viaIdP(idp, oidc.StartPath, "s-ali", "ali@firma.example"); got != "/" {
		t.Fatalf("ali: %q", got)
	}
	if rec := ali.do(http.MethodGet, "/api/auth/session", ""); strings.TrimSpace(rec.Body.String()) != `{"username":"ali","name":"Ali Kaya","role":"manager","all_stores":true,"stores":[]}` {
		t.Errorf("session: %s", rec.Body)
	}
	rec := ali.do(http.MethodGet, "/api/cashiers", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"ali"`) {
		t.Fatalf("kasiyerler: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Cashiers []cashierJSON `json:"cashiers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	linked := map[string]bool{}
	emails := map[string]string{}
	for _, c := range list.Cashiers {
		linked[c.ID], emails[c.ID] = c.Linked, c.Email
	}
	if !linked["ali"] || linked["veli"] || emails["ali"] != "ali@firma.example" || emails["veli"] != "" {
		t.Errorf("links: %v %v", linked, emails)
	}

	// A cashier does not get in even with a corporate identity; neither does one whose identity is not recognized.
	refused := "/?" + oidc.ErrorParam + "=" + oidc.CodeRefused
	zeynep := newBrowser(t, h)
	if got := zeynep.viaIdP(idp, oidc.StartPath, "s-zeynep", "zeynep@firma.example"); got != refused {
		t.Errorf("cashier: %q", got)
	}
	if rec := zeynep.do(http.MethodGet, "/api/cashiers", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session was opened for the cashier: %d", rec.Code)
	}
	if got := newBrowser(t, h).viaIdP(idp, oidc.StartPath, "s-yabanci", "yabanci@firma.example"); got != refused {
		t.Errorf("unregistered: %q", got)
	}

	// When the manager's PIN changes the session opened with the corporate sign-in closes too.
	if _, err := cashiers.SetPIN(ctx, "başka müdür", "ali", "4321"); err != nil {
		t.Fatal(err)
	}
	if rec := ali.do(http.MethodGet, "/api/cashiers", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the session stayed open when the PIN changed: %d", rec.Code)
	}
	// The identity is linked: they sign in again.
	ali = newBrowser(t, h)
	if got := ali.viaIdP(idp, oidc.StartPath, "s-ali", ""); got != "/" {
		t.Errorf("sign-in again: %q", got)
	}

	// A manager removes someone else's link from the screen; the person is matched with their address again.
	if rec := ali.do(http.MethodPost, "/api/cashiers/veli/identity/unlink", "{}"); rec.Code != http.StatusOK {
		t.Errorf("link removal: %d %s", rec.Code, rec.Body)
	}
	if rec := ali.do(http.MethodPost, "/api/cashiers/ali", `{"name":"Ali Kaya","role":"manager","email":"ali@firma.example"}`); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"linked":true`) {
		t.Errorf("the update broke the link: %d %s", rec.Code, rec.Body)
	}
	// The address is given from the screen: the same address cannot belong to two people, an invalid address is rejected.
	if rec := ali.do(http.MethodPost, "/api/cashiers/veli", `{"name":"Veli Er","role":"manager","email":"ALI@firma.example"}`); rec.Code != http.StatusConflict {
		t.Errorf("same address: %d %s", rec.Code, rec.Body)
	}
	if rec := ali.do(http.MethodPost, "/api/cashiers/veli", `{"name":"Veli Er","role":"manager","email":"veli"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid address: %d %s", rec.Code, rec.Body)
	}
	if rec := ali.do(http.MethodPost, "/api/cashiers/veli", `{"name":"Veli Er","role":"manager","email":"veli@firma.example"}`); rec.Code != http.StatusOK {
		t.Errorf("adres verme: %d %s", rec.Code, rec.Body)
	}
	veli := newBrowser(t, h)
	if got := veli.viaIdP(idp, oidc.StartPath, "s-veli", "veli@firma.example"); got != "/" {
		t.Errorf("veli: %q", got)
	}

	// The person signs in with their PIN, links another identity to their own account and removes it.
	pin := newBrowser(t, h)
	if rec := pin.do(http.MethodPost, "/api/auth/login", `{"cashier_id":"veli","pin":"5678"}`); rec.Code != http.StatusOK {
		t.Fatalf("PIN sign-in: %d %s", rec.Code, rec.Body)
	}
	if got := pin.viaIdP(idp, oidc.LinkPath, "s-veli-2", ""); got != "/?"+oidc.LinkedParam+"=1" {
		t.Errorf("linking: %q", got)
	}
	if rec := pin.do(http.MethodPost, oidc.UnlinkPath, "{}"); rec.Code != http.StatusNoContent {
		t.Errorf("removal: %d", rec.Code)
	}
}

// Whoever signs in with the store password has no personal account: they cannot link an identity.
func TestStorePasswordCannotLink(t *testing.T) {
	h, idp, cashiers := corporate(t, "yerel,oidc")
	for _, id := range []string{"ali", "veli"} {
		if _, err := cashiers.SetDisabled(context.Background(), "kurulum", id, true); err != nil {
			t.Fatal(err)
		}
	}
	// No manager with an open account is left: the store password opens again.
	b := newBrowser(t, h)
	if rec := b.do(http.MethodPost, "/api/auth/login", `{"password":"store-parolasi-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("store password: %d %s", rec.Code, rec.Body)
	}
	idp.Sub = "s-x"
	if rec := b.do(http.MethodGet, oidc.LinkPath, ""); rec.Code != http.StatusForbidden {
		t.Errorf("linking in a session with no person: %d", rec.Code)
	}
}

func TestCorporateOnly(t *testing.T) {
	h, idp, _ := corporate(t, "oidc")
	b := newBrowser(t, h)
	if rec := b.do(http.MethodGet, "/api/auth/methods", ""); strings.Contains(rec.Body.String(), "manager") || strings.Contains(rec.Body.String(), "password") ||
		!strings.Contains(rec.Body.String(), `"oidc"`) {
		t.Errorf("methods: %s", rec.Body)
	}
	// The PIN and store password endpoints are off.
	for _, body := range []string{`{"cashier_id":"ali","pin":"1234"}`, `{"password":"store-parolasi-1"}`} {
		if rec := b.do(http.MethodPost, "/api/auth/login", body); rec.Code != http.StatusForbidden || len(b.jar) != 0 {
			t.Errorf("%s: %d %v", body, rec.Code, b.jar)
		}
	}
	if got := b.viaIdP(idp, oidc.StartPath, "s-ali", "ali@firma.example"); got != "/" {
		t.Errorf("corporate sign-in: %q", got)
	}
}
