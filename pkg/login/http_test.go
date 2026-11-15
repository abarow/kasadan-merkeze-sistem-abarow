// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
)

// fake is the Authenticator's test adapter: the one whose password is "doğru" signs in;
// the passwords "kapalı", "kilit" and "bozuk" return the relevant error classes.
type fake struct{ sessions map[string]login.Person }

func (f *fake) Methods(context.Context) (login.Methods, error) {
	return login.Methods{Methods: []login.Method{{ID: "password", Kind: login.KindForm}}}, nil
}

func (f *fake) Login(_ context.Context, fields map[string]string) (string, login.Person, error) {
	switch fields["password"] {
	case "doğru":
		p := login.Person{Username: "ayse", Name: "Ayşe", Role: "admin", AllStores: true}
		token := login.NewToken()
		f.sessions[token] = p
		return token, p, nil
	case "kapalı":
		return "", login.Person{}, login.Refused("hesap kapalı")
	case "kilit":
		return "", login.Person{}, login.LockedError{Until: time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)}
	case "bozuk":
		return "", login.Person{}, errors.New("veritabanı kapalı")
	}
	return "", login.Person{}, login.Wrong("parola yanlış")
}

func (f *fake) Session(_ context.Context, token string) (login.Person, error) {
	if p, ok := f.sessions[token]; ok {
		return p, nil
	}
	return login.Person{}, login.ErrNoSession
}

func (f *fake) Logout(_ context.Context, token string) error {
	delete(f.sessions, token)
	return nil
}

func newWeb() http.Handler {
	w := login.Web{Auth: &fake{sessions: map[string]login.Person{}}, Cookie: login.Cookie{Name: "test", TTL: time.Hour}}
	mux := http.NewServeMux()
	w.Handle(mux)
	mux.HandleFunc("POST /yaz", w.Require(func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprint(rw, login.FromRequest(r).Name)
	}))
	mux.Handle("/arka", w.Forward(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p, _ := login.PersonFrom(r.Header)
		fmt.Fprintf(rw, "%s|%s|%v", p.Username, p.Role, p.AllStores)
	})))
	return mux
}

func do(h http.Handler, method, path, contentType, body string, cookie *http.Cookie, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestLoginStatuses(t *testing.T) {
	h := newWeb()
	for _, c := range []struct {
		password string
		code     int
		contains string
	}{
		{"doğru", 200, `"username":"ayse"`},
		{"yanlış", 401, "parola yanlış"},
		{"kapalı", 403, "hesap kapalı"},
		{"kilit", 429, "10:30"},
		{"bozuk", 503, "giriş yapılamadı: veritabanı kapalı"},
	} {
		rec := do(h, "POST", "/api/auth/login", "application/json", `{"password":"`+c.password+`"}`, nil)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("%s: %d %s; want %d %q", c.password, rec.Code, rec.Body, c.code, c.contains)
		}
		if got := rec.Header().Get("Set-Cookie"); (c.code == 200) != (got != "") {
			t.Errorf("%s: cookie %q", c.password, got)
		}
	}
	if rec := do(h, "POST", "/api/auth/login", "", `{"password":"doğru"}`, nil); rec.Code != 415 {
		t.Errorf("non-JSON sign-in: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/auth/login", "application/json", `{`, nil); rec.Code != 400 {
		t.Errorf("corrupt body: %d", rec.Code)
	}
}

func TestSessionRequireForward(t *testing.T) {
	h := newWeb()
	if rec := do(h, "GET", "/api/auth/session", "", "", nil); rec.Code != 401 {
		t.Fatalf("session without sign-in: %d", rec.Code)
	}
	cookie := do(h, "POST", "/api/auth/login", "application/json", `{"password":"doğru"}`, nil).Result().Cookies()[0]
	if rec := do(h, "GET", "/api/auth/session", "", "", cookie); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"stores":[]`) {
		t.Errorf("session: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/yaz", "", "", nil); rec.Code != 401 {
		t.Errorf("write without sign-in: %d", rec.Code)
	}
	if rec := do(h, "POST", "/yaz", "text/plain", "x", cookie); rec.Code != 415 {
		t.Errorf("JSON olmayan yazma: %d", rec.Code)
	}
	if rec := do(h, "POST", "/yaz", "application/json", "{}", cookie); rec.Code != 200 || rec.Body.String() != "Ayşe" {
		t.Errorf("yazma: %d %s", rec.Code, rec.Body)
	}
	// A person header put by the client counts as fake, the person in the session passes.
	rec := do(h, "GET", "/arka", "", "", cookie, login.UserHeader, "mallory", login.RoleHeader, "report")
	if rec.Body.String() != "ayse|admin|true" {
		t.Errorf("person passed to the backend service: %q", rec.Body)
	}
	if rec := do(h, "GET", "/arka", "", "", nil, login.UserHeader, "mallory"); rec.Code != 401 {
		t.Errorf("forwarding without sign-in: %d", rec.Code)
	}

	if rec := do(h, "POST", "/api/auth/logout", "", "", cookie); rec.Code != 415 {
		t.Errorf("non-JSON sign-out: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/auth/logout", "application/json", "{}", cookie); rec.Code != 204 {
		t.Errorf("sign-out: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/auth/session", "", "", cookie); rec.Code != 401 {
		t.Errorf("session after sign-out: %d", rec.Code)
	}
}

func TestForwardNamelessSession(t *testing.T) {
	// In a session not tied to a person the name stands in for the username.
	f := &fake{sessions: map[string]login.Person{"t": {Name: "mağaza parolası", Role: "manager", AllStores: true}}}
	w := login.Web{Auth: f, Cookie: login.Cookie{Name: "test", TTL: time.Hour}}
	h := w.Forward(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p, ok := login.PersonFrom(r.Header)
		fmt.Fprintf(rw, "%v|%s", ok, p.Name)
	}))
	rec := do(h, "GET", "/", "", "", &http.Cookie{Name: "test", Value: "t"})
	if rec.Body.String() != "true|mağaza parolası" {
		t.Errorf("person forwarded: %q", rec.Body)
	}
}

func TestStatusWrapped(t *testing.T) {
	err := fmt.Errorf("sarılı: %w", login.Refused("kapalı"))
	if login.Status(err) != 403 || !errors.Is(err, login.ErrRefused) {
		t.Errorf("wrapped Refused: %d", login.Status(err))
	}
	if login.Status(login.ErrNoSession) != 401 {
		t.Error("ErrNoSession should be 401")
	}
}
