// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc/oidctest"
	"abarow.com/pkg/personnel"
	"abarow.com/pkg/personnel/personneltest"
)

var sessionCookie = login.Cookie{Name: "abarow_test", TTL: time.Hour}

// localAuth stands in for the service's own sign-in adapter: a single password form.
type localAuth struct{ sessions *login.Sessions }

func (localAuth) Methods(context.Context) (login.Methods, error) {
	return login.Methods{Methods: []login.Method{{ID: "password", Kind: login.KindForm, Label: "Parolayla gir"}}}, nil
}

func (l localAuth) Login(ctx context.Context, f map[string]string) (string, login.Person, error) {
	token, err := l.sessions.Open(ctx, f["username"], nil)
	return token, login.Person{Username: f["username"]}, err
}

func (l localAuth) Session(ctx context.Context, token string) (login.Person, error) {
	return l.sessions.Person(ctx, token)
}

func (l localAuth) Logout(ctx context.Context, token string) error {
	return l.sessions.Close(ctx, token)
}

// site is a service set up with the corporate sign-in.
type site struct {
	t        *testing.T
	idp      *oidctest.IdP
	people   *personnel.Manage
	store    *personneltest.Store
	sessions *login.Sessions
	corp     *Corporate
	mux      *http.ServeMux
}

var (
	root = personnel.Actor{ID: "ayse", Name: "Ayşe", Manage: true}
	ctx  = context.Background()
)

func newSite(t *testing.T, methods string) *site {
	t.Helper()
	s := &site{t: t, idp: oidctest.New(t, clientID), store: personneltest.New(), mux: http.NewServeMux()}
	s.people = personnel.NewManage(s.store, personnel.Config{
		Roles: []personnel.Role{personnel.RoleAdmin, personnel.RoleReport}, Admins: []personnel.Role{personnel.RoleAdmin},
	})
	s.people.HashPassword = func(p string) (string, error) { return "ozet:" + p, nil }
	s.sessions = login.NewSessions(s.store, directory{s.store}, time.Hour, nil)
	for id, email := range map[string]string{"ayse": "ayse@firma.example", "ali": "ali@firma.example", "veli": ""} {
		if _, err := s.people.Add(ctx, root, id, "uzun-parola-12", personnel.Input{Role: personnel.RoleAdmin, Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{
		"ABAROW_LOGIN": methods, "ABAROW_OIDC_ISSUER": s.idp.URL(), "ABAROW_OIDC_CLIENT_ID": clientID,
		"ABAROW_OIDC_REDIRECT_URL": "http://localhost:8080" + CallbackPath,
	}
	corp, err := Configure(func(k string) string { return env[k] }, Service{
		People: s.people, Cookie: sessionCookie,
		Open: func(ctx context.Context, u personnel.User) (string, login.Person, error) {
			token, err := s.sessions.Open(ctx, u.ID, nil)
			return token, login.Person{Username: u.ID, Name: u.Name, Role: string(u.Role)}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.corp = corp
	web := login.Web{Auth: corp.Wrap(localAuth{s.sessions}), Cookie: sessionCookie}
	web.Handle(s.mux)
	corp.Handle(s.mux, web)
	return s
}

// directory reads the person in the session from the record; "" is a store password session.
type directory struct{ store *personneltest.Store }

func (d directory) Resolve(ctx context.Context, s login.Session) (login.Person, error) {
	if s.Subject == "" {
		return login.Person{Name: "Mağaza parolası"}, nil
	}
	u, err := d.store.User(ctx, s.Subject)
	if err != nil || u.Disabled {
		return login.Person{}, login.ErrNoSession
	}
	return login.Person{Username: u.ID, Name: u.Name, Role: string(u.Role)}, nil
}

func (s *site) do(method, path string, jar map[string]string, json bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if json {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range jar {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(jar, c.Name)
		} else {
			jar[c.Name] = c.Value
		}
	}
	return w
}

// viaIdP is the browser starting from the given first endpoint, confirming the identity (sub, email)
// at the IdP and returning; it returns the final redirect.
func (s *site) viaIdP(jar map[string]string, first, sub, email string) string {
	s.t.Helper()
	s.idp.Sub, s.idp.Email = sub, email
	w := s.do("GET", first, jar, false)
	if w.Code != http.StatusSeeOther {
		s.t.Fatalf("%s: status %d: %s", first, w.Code, w.Body)
	}
	cb, _ := s.idp.Authorize(s.t, w.Header().Get("Location"))
	u, _ := url.Parse(cb)
	w = s.do("GET", u.RequestURI(), jar, false)
	if w.Code != http.StatusSeeOther {
		s.t.Fatalf("return: status %d", w.Code)
	}
	return w.Header().Get("Location")
}

func (s *site) who(jar map[string]string) (int, login.PersonJSON) {
	w := s.do("GET", "/api/auth/session", jar, false)
	var p login.PersonJSON
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w.Code, p
}

func (s *site) linked(jar map[string]string) bool {
	w := s.do("GET", AccountPath, jar, false)
	var a AccountJSON
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &a) != nil {
		s.t.Fatalf("hesap durumu: %d %s", w.Code, w.Body)
	}
	return a.Linked
}

func TestCorporateLogin(t *testing.T) {
	s := newSite(t, "yerel,oidc")

	// Sign-in screen: the service's own method and the corporate sign-in button.
	w := s.do("GET", "/api/auth/methods", map[string]string{}, false)
	var m login.Methods
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || len(m.Methods) != 2 ||
		m.Methods[0].ID != "password" || m.Methods[1].ID != "oidc" || m.Methods[1].Kind != login.KindRedirect || m.Methods[1].URL != StartPath {
		t.Fatalf("methods: %s %v", w.Body, err)
	}

	// First sign-in: the verified address selects the person, the identity is linked.
	jar := map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-ayse", "Ayse@Firma.example"); got != "/" {
		t.Fatalf("first sign-in: %q", got)
	}
	if code, p := s.who(jar); code != http.StatusOK || p.Username != "ayse" || p.Role != "admin" {
		t.Errorf("session: %d %+v", code, p)
	}
	if u, _ := s.store.User(ctx, "ayse"); u.Subject != "s-ayse" || u.Issuer != s.idp.URL() {
		t.Errorf("identity was not linked: %+v", u)
	}

	// After that the identity determines the person: they sign in even if the IdP address changes.
	jar = map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-ayse", "tamamen-baska@firma.example"); got != "/" {
		t.Errorf("linked identity: %q", got)
	}
	if _, p := s.who(jar); p.Username != "ayse" {
		t.Errorf("session: %+v", p)
	}

	// An unregistered identity, an identity with no address, another identity that names a linked
	// person's address, an IdP that does not verify the address: rejected, no session is opened.
	refused := "/?" + ErrorParam + "=" + CodeRefused
	denied := func(name, sub, email string) {
		t.Helper()
		jar := map[string]string{}
		if got := s.viaIdP(jar, StartPath, sub, email); got != refused {
			t.Errorf("%s: %q", name, got)
		}
		if code, _ := s.who(jar); code != http.StatusUnauthorized {
			t.Errorf("%s: a session was opened for the rejected", name)
		}
	}
	denied("kayıtsız", "s-x", "kimse@firma.example")
	denied("adressiz", "s-y", "")
	denied("bağlı kişinin adresi", "s-z", "ayse@firma.example")
	s.idp.Verified = false
	denied("doğrulanmamış adres", "s-ayse2", "ali@firma.example")
	if u, _ := s.store.User(ctx, "ali"); u.Subject != "" {
		t.Errorf("an unverified address linked an identity: %+v", u)
	}

	// A closed account does not sign in even if its identity is linked.
	s.idp.Verified = true
	jar = map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-ali", "ali@firma.example"); got != "/" {
		t.Fatalf("ali: %q", got)
	}
	if _, err := s.people.SetDisabled(ctx, root, "ali", true); err != nil {
		t.Fatal(err)
	}
	jar = map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-ali", "ali@firma.example"); got != refused {
		t.Errorf("closed account: %q", got)
	}
}

func TestLinkIdentity(t *testing.T) {
	s := newSite(t, "yerel,oidc")
	veli := map[string]string{}
	token, err := s.sessions.Open(ctx, "veli", nil)
	if err != nil {
		t.Fatal(err)
	}
	veli[sessionCookie.Name] = token

	// Not linked; linking redirects to the IdP, on return the session stays the same.
	if s.linked(veli) {
		t.Fatal("a person with no link looks linked")
	}
	got := s.viaIdP(veli, LinkPath, "s-veli", "veli@firma.example")
	if got != "/?"+LinkedParam+"=1" {
		t.Fatalf("linking: %q", got)
	}
	if veli[sessionCookie.Name] != token {
		t.Error("linking changed the session cookie")
	}
	if !s.linked(veli) {
		t.Error("does not look linked after linking")
	}
	if u, _ := s.store.User(ctx, "veli"); u.Subject != "s-veli" {
		t.Errorf("record: %+v", u)
	}

	// After linking they sign in with the corporate sign-in.
	jar := map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-veli", ""); got != "/" {
		t.Errorf("corporate sign-in: %q", got)
	}
	if _, p := s.who(jar); p.Username != "veli" {
		t.Errorf("session: %+v", p)
	}

	// An identity linked to someone else cannot be taken.
	ali := map[string]string{}
	atoken, _ := s.sessions.Open(ctx, "ali", nil)
	ali[sessionCookie.Name] = atoken
	if got := s.viaIdP(ali, LinkPath, "s-veli", ""); got != "/?"+ErrorParam+"="+CodeTaken {
		t.Errorf("identity taken: %q", got)
	}
	if s.linked(ali) {
		t.Error("a taken identity was linked")
	}

	// There is no linking without a session; a store password session is not a personal account.
	for _, path := range []string{LinkPath, AccountPath} {
		if w := s.do("GET", path, map[string]string{}, false); w.Code != http.StatusUnauthorized {
			t.Errorf("%s oturumsuz: %d", path, w.Code)
		}
	}
	storeToken, _ := s.sessions.Open(ctx, "", nil)
	store := map[string]string{sessionCookie.Name: storeToken}
	if w := s.do("GET", LinkPath, store, false); w.Code != http.StatusForbidden {
		t.Errorf("linking in a session with no person: %d", w.Code)
	}
	if s.linked(store) {
		t.Error("a session with no person looks linked")
	}

	// Removing the link: only a JSON POST, with a session.
	if w := s.do("POST", UnlinkPath, veli, false); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("removal without JSON: %d", w.Code)
	}
	if w := s.do("POST", UnlinkPath, map[string]string{}, true); w.Code != http.StatusUnauthorized {
		t.Errorf("removal without a session: %d", w.Code)
	}
	if w := s.do("POST", UnlinkPath, veli, true); w.Code != http.StatusNoContent {
		t.Fatalf("removal: %d", w.Code)
	}
	if s.linked(veli) {
		t.Error("the removed link still shows")
	}
	jar = map[string]string{}
	if got := s.viaIdP(jar, StartPath, "s-veli", ""); got != "/?"+ErrorParam+"="+CodeRefused {
		t.Errorf("identity whose link was removed: %q", got)
	}
}

func TestOIDCOnly(t *testing.T) {
	s := newSite(t, "oidc")
	jar := map[string]string{}
	w := s.do("GET", "/api/auth/methods", jar, false)
	var m login.Methods
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || len(m.Methods) != 1 || m.Methods[0].ID != "oidc" {
		t.Fatalf("methods: %s %v", w.Body, err)
	}
	// The service's own sign-in endpoint is closed too: hiding it from the screen is not enough.
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"ayse"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
		t.Errorf("local sign-in: %d %v", rec.Code, rec.Result().Cookies())
	}
	if got := s.viaIdP(jar, StartPath, "s-ayse", "ayse@firma.example"); got != "/" {
		t.Errorf("corporate sign-in: %q", got)
	}
}

func TestConfigure(t *testing.T) {
	good := map[string]string{
		"ABAROW_LOGIN": "yerel,oidc", "ABAROW_OIDC_ISSUER": "https://idp.example", "ABAROW_OIDC_CLIENT_ID": "c",
		"ABAROW_OIDC_REDIRECT_URL": "https://m.example" + CallbackPath,
	}
	with := func(mod map[string]string) func(string) string {
		env := map[string]string{}
		for k, v := range good {
			env[k] = v
		}
		for k, v := range mod {
			env[k] = v
		}
		return func(k string) string { return env[k] }
	}
	svc := Service{Cookie: sessionCookie}
	local := localAuth{}

	if c, err := Configure(with(nil), svc); err != nil || c == nil {
		t.Errorf("valid setting: %v %v", c, err)
	}
	for name, mod := range map[string]map[string]string{
		"yerel": {"ABAROW_LOGIN": "yerel"},
		"boş":   {"ABAROW_LOGIN": ""},
	} {
		c, err := Configure(with(mod), svc)
		if err != nil || c != nil {
			t.Errorf("%s: corporate sign-in was set up: %v %v", name, c, err)
		}
		// If it is not installed the service's own sign-in stays as it is.
		if c.Wrap(local) != login.Authenticator(local) {
			t.Errorf("%s: the adapter was wrapped", name)
		}
		c.Handle(http.NewServeMux(), login.Web{}) // nil: does nothing
	}
	for name, mod := range map[string]map[string]string{
		"oidc ama IdP yok":  {"ABAROW_OIDC_ISSUER": ""},
		"geçersiz kip":      {"ABAROW_LOGIN": "ldap"},
		"istemci kimliksiz": {"ABAROW_OIDC_CLIENT_ID": ""},
		"düz HTTP IdP":      {"ABAROW_OIDC_ISSUER": "http://idp.example"},
		"yanlış geri dönüş": {"ABAROW_OIDC_REDIRECT_URL": "https://m.example/login"},
	} {
		if c, err := Configure(with(mod), svc); err == nil {
			t.Errorf("%s: the setting was accepted (%v)", name, c)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]login.Mode{
		"": {Local: true}, "yerel": {Local: true}, "oidc": {OIDC: true},
		"yerel,oidc": {Local: true, OIDC: true}, "oidc, yerel": {Local: true, OIDC: true},
		"oidc,oidc": {OIDC: true}, ",": {Local: true},
	} {
		if got, err := login.ParseMode(in); err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"ldap", "yerel;oidc", "OIDC"} {
		if _, err := login.ParseMode(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}
