// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"abarow.com/pkg/login"
	"abarow.com/services/store/internal/app"
)

// The stock requests are forwarded to inventory with the session; report puts the person's name,
// a header of the same name coming from the screen does not pass. If inventory is not installed the
// request is rejected with an explanation.
func TestStockProxy(t *testing.T) {
	var got struct{ path, query, person string }
	inventory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.query = r.URL.Path, r.URL.RawQuery
		got.person, _ = url.QueryUnescape(r.Header.Get(login.PersonHeader))
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	}))
	defer inventory.Close()
	target, _ := url.Parse(inventory.URL)
	hash, err := app.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := app.NewAuth(hash, login.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul), auth, nil, t.TempDir(), nil, nil, target))
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/api/stock"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("oturumsuz: %v %v", resp.StatusCode, err)
	}
	c := client(t)
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/stock?q=ekmek", nil)
	req.Header.Set(login.PersonHeader, "sahte")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || got.path != "/api/stock" || got.query != "q=ekmek" || got.person != app.StorePasswordName {
		t.Errorf("iletim: %d %+v", resp.StatusCode, got)
	}

	var e struct{ Error string }
	if code := get(t, newHandler(t, app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)), "/api/stock", &e); code != http.StatusNotFound || e.Error != "stok modülü kurulu değil" {
		t.Errorf("no inventory: %d %q", code, e.Error)
	}
}

// The update agent's status is forwarded with the session; if the agent is not installed it is rejected with an explanation.
func TestUpdateStatusProxy(t *testing.T) {
	var got struct{ path, secret string }
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.secret = r.URL.Path, r.Header.Get(login.UpdateSecretHeader)
		writeJSON(w, http.StatusOK, map[string]any{"state": "pending", "code": "ABCD-2345"})
	}))
	defer agent.Close()
	target, _ := url.Parse(agent.URL)
	hash, err := app.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := app.NewAuth(hash, login.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)
	srv := httptest.NewServer(New(svc, auth, nil, t.TempDir(), nil, nil, nil, WithUpdate(target, "gizli")))
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/api/update/status"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("oturumsuz: %v %v", resp.StatusCode, err)
	}
	c := client(t)
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}
	resp, err := c.Get(srv.URL + "/api/update/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || got.path != "/api/update/status" {
		t.Errorf("iletim: %d %+v", resp.StatusCode, got)
	}

	// Manager approval: it goes to the agent with the secret key; a key the client puts is ignored.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/update/apply", strings.NewReader(`{"seq":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(login.UpdateSecretHeader, "sahte")
	if resp, err := c.Do(req); err != nil || resp.StatusCode != http.StatusOK || got.path != "/api/update/apply" || got.secret != "gizli" {
		t.Errorf("approval: %v %v %+v", resp, err, got)
	}
	got.secret = "kalan"
	if resp, err := c.Get(srv.URL + "/api/update/status"); err != nil || resp.StatusCode != http.StatusOK || got.secret != "" {
		t.Errorf("the secret key was added to the status request: %v %v %q", resp, err, got.secret)
	}

	var e struct{ Error string }
	if code := get(t, newHandler(t, svc), "/api/update/status", &e); code != http.StatusNotFound || e.Error != "güncelleme kurulu değil" {
		t.Errorf("no agent: %d %q", code, e.Error)
	}
}
