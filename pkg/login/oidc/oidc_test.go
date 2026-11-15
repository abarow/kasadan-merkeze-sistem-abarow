// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc/oidctest"
)

const clientID = "abarow-test"

// rig connects the flow to a fake IdP; the login calls are recorded.
type rig struct {
	t        *testing.T
	idp      *oidctest.IdP
	flow     *Flow
	mux      *http.ServeMux
	now      time.Time
	seen     []Claims
	loginErr error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, idp: oidctest.New(t, clientID), mux: http.NewServeMux(), now: time.Now()}
	open := func(_ context.Context, c Claims) (string, login.Person, error) {
		g.seen = append(g.seen, c)
		if g.loginErr != nil {
			return "", login.Person{}, g.loginErr
		}
		return "oturum-anahtari", login.Person{Username: "ayse", Name: "Ayşe"}, nil
	}
	flow, err := New(Config{
		Issuer: g.idp.URL(), ClientID: clientID, ClientSecret: "sir",
		RedirectURL: "http://localhost:8080" + CallbackPath, Label: "Firma girişi",
	}, open, login.Cookie{Name: "abarow_test", TTL: time.Hour}, WithClock(func() time.Time { return g.now }))
	if err != nil {
		t.Fatal(err)
	}
	g.flow = flow
	flow.Handle(g.mux)
	return g
}

// do gives the request to the flow; cookies is the browser's cookie jar.
func (g *rig) do(path string, jar map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range jar {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(jar, c.Name)
		} else {
			jar[c.Name] = c.Value
		}
	}
	return w
}

// begin starts the sign-in and gives the approval at the IdP; it returns the return path.
func (g *rig) begin(jar map[string]string) string {
	g.t.Helper()
	w := g.do(StartPath, jar)
	if w.Code != http.StatusSeeOther {
		g.t.Fatalf("start: status %d", w.Code)
	}
	cb, _ := g.idp.Authorize(g.t, w.Header().Get("Location"))
	u, _ := url.Parse(cb)
	return u.RequestURI()
}

func wantRedirect(t *testing.T, w *httptest.ResponseRecorder, loc string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != loc {
		t.Errorf("redirect: %d %q, want %q", w.Code, w.Header().Get("Location"), loc)
	}
}

func TestLoginFlow(t *testing.T) {
	g := newRig(t)
	jar := map[string]string{}

	w := g.do(StartPath, jar)
	var flowCk *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == flowCookie {
			flowCk = c
		}
	}
	if flowCk == nil || !flowCk.HttpOnly || flowCk.SameSite != http.SameSiteLaxMode || flowCk.Path != CallbackPath {
		t.Fatalf("flow cookie: %+v", flowCk)
	}
	if strings.Contains(flowCk.Value, g.idp.Sub) || len(flowCk.Value) > 1024 {
		t.Errorf("the flow cookie should be sealed and small: %q", flowCk.Value)
	}
	cb, _ := g.idp.Authorize(t, w.Header().Get("Location"))
	u, _ := url.Parse(cb)

	w = g.do(u.RequestURI(), jar)
	wantRedirect(t, w, "/")
	if jar["abarow_test"] != "oturum-anahtari" {
		t.Errorf("session cookie: %v", jar)
	}
	if _, left := jar[flowCookie]; left {
		t.Error("the flow cookie was not deleted after use")
	}
	want := Claims{Issuer: g.idp.URL(), Subject: "sub-1", Email: "ayse@firma.example", EmailVerified: true}
	if len(g.seen) != 1 || g.seen[0] != want {
		t.Errorf("identity: %+v, want %+v", g.seen, want)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("the response can be cached")
	}
}

func TestEmailVerifiedAsString(t *testing.T) {
	for in, want := range map[any]bool{"true": true, "false": false, true: true, false: false, nil: false} {
		g := newRig(t)
		g.idp.Verified = in
		jar := map[string]string{}
		wantRedirect(t, g.do(g.begin(jar), jar), "/")
		if g.seen[0].EmailVerified != want {
			t.Errorf("email_verified=%v: %v, want %v", in, g.seen[0].EmailVerified, want)
		}
	}
}

func TestCallbackRejects(t *testing.T) {
	denied := "/?" + ErrorParam + "=" + CodeDenied
	expired := "/?" + ErrorParam + "=" + CodeExpired
	for _, tc := range []struct {
		name   string
		setup  func(g *rig)
		tamper func(g *rig, path string, jar map[string]string) string
		want   string
	}{
		{"akış başlatılmamış", nil, func(g *rig, path string, jar map[string]string) string {
			clear(jar)
			return path
		}, expired},
		{"state uyuşmuyor", nil, func(g *rig, path string, jar map[string]string) string {
			return strings.Replace(path, "state=", "state=x", 1)
		}, expired},
		{"çerez bozuk", nil, func(g *rig, path string, jar map[string]string) string {
			jar[flowCookie] = jar[flowCookie][:len(jar[flowCookie])-3] + "AAA"
			return path
		}, expired},
		{"akış süresi doldu", nil, func(g *rig, path string, jar map[string]string) string {
			g.now = g.now.Add(flowTTL + time.Second)
			return path
		}, expired},
		{"IdP hata döndü", nil, func(g *rig, path string, jar map[string]string) string {
			return CallbackPath + "?error=access_denied&state=" + url.QueryEscape(stateOf(path))
		}, denied},
		{"kod yok", nil, func(g *rig, path string, jar map[string]string) string {
			return CallbackPath + "?state=" + url.QueryEscape(stateOf(path))
		}, denied},
		{"nonce uyuşmuyor", func(g *rig) { g.idp.Nonce = "baska" }, nil, denied},
		{"jeton başka istemci için", func(g *rig) { g.idp.Audience = "baska-istemci" }, nil, denied},
		{"jeton süresi dolmuş", func(g *rig) { g.idp.Expiry = -time.Hour }, nil, denied},
		{"jeton başka anahtarla imzalı", func(g *rig) { g.idp.BadKey = true }, nil, denied},
		{"sub yok", func(g *rig) { g.idp.Sub = "" }, nil, denied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t)
			if tc.setup != nil {
				tc.setup(g)
			}
			jar := map[string]string{}
			path := g.begin(jar)
			if tc.tamper != nil {
				path = tc.tamper(g, path, jar)
			}
			w := g.do(path, jar)
			wantRedirect(t, w, tc.want)
			if len(g.seen) != 0 || jar["abarow_test"] != "" {
				t.Errorf("a session was opened in the rejected flow: %v %v", g.seen, jar)
			}
		})
	}
}

func stateOf(path string) string {
	u, _ := url.Parse(path)
	return u.Query().Get("state")
}

func TestCodeCannotBeReplayed(t *testing.T) {
	g := newRig(t)
	jar := map[string]string{}
	path := g.begin(jar)
	saved := map[string]string{flowCookie: jar[flowCookie]}
	wantRedirect(t, g.do(path, jar), "/")

	// The captured cookie and code are sent once more: the IdP lets a code pass once,
	// the flow is rejected.
	wantRedirect(t, g.do(path, saved), "/?"+ErrorParam+"="+CodeUnavailable)
	if len(g.seen) != 1 {
		t.Errorf("the code opened a session a second time: %v", g.seen)
	}
}

func TestLoginRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"bağlı hesap yok":  {login.Refused("kayıtlı değil"), CodeRefused},
		"yanlış":           {login.Wrong("bu kimlik tanınmıyor"), CodeRefused},
		"kayıt erişilemez": {errors.New("veritabanı kapalı"), CodeUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			g.loginErr = tc.err
			jar := map[string]string{}
			wantRedirect(t, g.do(g.begin(jar), jar), "/?"+ErrorParam+"="+tc.want)
			if jar["abarow_test"] != "" {
				t.Error("a session cookie was given to the rejected person")
			}
		})
	}
}

func TestIdPDownThenUp(t *testing.T) {
	g := newRig(t)
	g.idp.SetDown(true)
	jar := map[string]string{}
	wantRedirect(t, g.do(StartPath, jar), "/?"+ErrorParam+"="+CodeUnavailable)

	// When the IdP comes back the sign-in works without restarting the service.
	g.idp.SetDown(false)
	wantRedirect(t, g.do(g.begin(jar), jar), "/")
}

func TestMethod(t *testing.T) {
	g := newRig(t)
	m := g.flow.Method()
	if m.ID != "oidc" || m.Kind != login.KindRedirect || m.URL != StartPath || m.Label != "Firma girişi" {
		t.Errorf("method: %+v", m)
	}
}

func TestConfig(t *testing.T) {
	good := Config{Issuer: "https://idp.example/realms/firma", ClientID: "c", RedirectURL: "https://headquarters.example" + CallbackPath}
	open := func(context.Context, Claims) (string, login.Person, error) { return "", login.Person{}, nil }
	if _, err := New(good, open, login.Cookie{}); err != nil {
		t.Fatalf("valid setting: %v", err)
	}
	for name, mod := range map[string]func(*Config){
		"issuer HTTP":             func(c *Config) { c.Issuer = "http://idp.example" },
		"issuer yok":              func(c *Config) { c.Issuer = "" },
		"issuer göreli":           func(c *Config) { c.Issuer = "idp.example" },
		"client id yok":           func(c *Config) { c.ClientID = "" },
		"yönlendirme HTTP":        func(c *Config) { c.RedirectURL = "http://headquarters.example" + CallbackPath },
		"yönlendirme yolu yanlış": func(c *Config) { c.RedirectURL = "https://headquarters.example/login" },
		"yönlendirmede sorgu":     func(c *Config) { c.RedirectURL = "https://headquarters.example" + CallbackPath + "?x=1" },
		"yönlendirme göreli":      func(c *Config) { c.RedirectURL = CallbackPath },
	} {
		c := good
		mod(&c)
		if _, err := New(c, open, login.Cookie{}); err == nil {
			t.Errorf("%s: the setting was accepted", name)
		}
	}
	// For development HTTP is allowed on a loopback address.
	c := good
	c.Issuer, c.RedirectURL = "http://127.0.0.1:8081", "http://localhost:8080"+CallbackPath
	if _, err := New(c, open, login.Cookie{}); err != nil {
		t.Errorf("loopback HTTP: %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "sir")
	if err := os.WriteFile(secret, []byte("gizli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"ABAROW_OIDC_ISSUER": "https://idp.example", "ABAROW_OIDC_CLIENT_ID": "c",
		"ABAROW_OIDC_CLIENT_SECRET_FILE": secret, "ABAROW_OIDC_REDIRECT_URL": "https://m.example" + CallbackPath,
		"ABAROW_OIDC_LABEL": "Firma",
	}
	get := func(k string) string { return env[k] }

	cfg, on, err := LoadConfig(get)
	want := Config{Issuer: "https://idp.example", ClientID: "c", ClientSecret: "gizli", RedirectURL: "https://m.example" + CallbackPath, Label: "Firma"}
	if err != nil || !on || cfg != want {
		t.Errorf("ayar: %+v %v %v", cfg, on, err)
	}

	env["ABAROW_OIDC_CLIENT_SECRET_FILE"] = filepath.Join(t.TempDir(), "yok")
	if _, _, err := LoadConfig(get); err == nil {
		t.Error("a missing secret file was accepted")
	}
	delete(env, "ABAROW_OIDC_CLIENT_SECRET_FILE")
	if cfg, _, err := LoadConfig(get); err != nil || cfg.ClientSecret != "" {
		t.Errorf("open client without a secret: %+v %v", cfg, err)
	}
	env["ABAROW_OIDC_CLIENT_ID"] = ""
	if _, _, err := LoadConfig(get); err == nil {
		t.Error("a setting without a client id was accepted")
	}
	if _, on, err := LoadConfig(func(string) string { return "" }); on || err != nil {
		t.Errorf("kurulu olmayan OIDC: %v %v", on, err)
	}
}
