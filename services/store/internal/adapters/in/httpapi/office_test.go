// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"abarow.com/pkg/login"
	"abarow.com/services/store/internal/app"
)

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) Setting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *memSettings) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

type okStream struct{}

func (okStream) Apply(context.Context, bool) error { return nil }

// catalogBackend stands in for catalog-admin: it records the person and the method
// that come.
type catalogBackend struct {
	mu      sync.Mutex
	methods []string
	persons []login.Person
}

func (c *catalogBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.methods = append(c.methods, r.Method+" "+r.URL.Path)
	p, _ := login.PersonFrom(r.Header)
	c.persons = append(c.persons, p)
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func (c *catalogBackend) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.methods)
}

// officeServer sets up a store server screen and returns a client that has signed in.
func officeServer(t *testing.T, installed bool) (*httptest.Server, *http.Client, *catalogBackend) {
	t.Helper()
	backend := &catalogBackend{}
	cat := httptest.NewServer(backend)
	t.Cleanup(cat.Close)
	target, _ := url.Parse(cat.URL)

	hash, err := app.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := app.NewAuth(hash, login.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	office := app.NewHeadquarters(&memSettings{}, okStream{}, installed)
	svc := app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)
	srv := httptest.NewServer(New(svc, auth, nil, t.TempDir(), nil, nil, nil, WithCatalog(target, office)))
	t.Cleanup(srv.Close)

	c := client(t)
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}
	return srv, c, backend
}

func call(t *testing.T, c *http.Client, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// While headquarters is connected the catalog can be read but not written; when the setting is turned off writing
// opens, when reconnected it closes.
func TestCatalogReadOnlyWhileConnected(t *testing.T) {
	srv, c, backend := officeServer(t, true)

	var st officeJSON
	code, body := call(t, c, "GET", srv.URL+"/api/settings/headquarters", "")
	if err := json.Unmarshal([]byte(body), &st); err != nil || code != 200 || !st.Installed || !st.Connected || st.CatalogWritable {
		t.Fatalf("ilk durum: %d %s", code, body)
	}
	if code, _ := call(t, c, "GET", srv.URL+"/api/catalog/products", ""); code != 200 || backend.count() != 1 {
		t.Errorf("the read was not forwarded while connected: %d (%d requests)", code, backend.count())
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if code, body := call(t, c, m, srv.URL+"/api/catalog/products/ekmek", `{}`); code != http.StatusForbidden || !strings.Contains(body, "merkezden") {
			t.Errorf("%s while connected: %d %s", m, code, body)
		}
	}
	if backend.count() != 1 {
		t.Errorf("the rejected write reached catalog-admin: %d requests", backend.count())
	}

	// The manager turns off the headquarters connection.
	code, body = call(t, c, "POST", srv.URL+"/api/settings/headquarters", `{"connected":false}`)
	if err := json.Unmarshal([]byte(body), &st); err != nil || code != 200 || st.Connected || !st.CatalogWritable || st.Applied == nil || !*st.Applied {
		t.Fatalf("kapatma: %d %s", code, body)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/catalog/products", `{"code":"ekmek"}`); code != 200 {
		t.Errorf("kopukken yazma: %d", code)
	}
	last := backend.persons[len(backend.persons)-1]
	if last.Role != "manager" || last.Name == "" {
		t.Errorf("person that went to catalog-admin: %+v", last)
	}

	// When reconnected it is read-only again.
	call(t, c, "POST", srv.URL+"/api/settings/headquarters", `{"connected":true}`)
	if code, _ := call(t, c, "POST", srv.URL+"/api/catalog/products", `{"code":"x"}`); code != http.StatusForbidden {
		t.Errorf("write while reconnected: %d", code)
	}
}

// If headquarters is not installed it cannot be connected; the store is disconnected and the catalog
// can be written.
func TestOfficeNotInstalled(t *testing.T) {
	srv, c, _ := officeServer(t, false)
	if code, body := call(t, c, "POST", srv.URL+"/api/settings/headquarters", `{"connected":true}`); code != http.StatusConflict {
		t.Errorf("connecting to a headquarters that is not installed: %d %s", code, body)
	}
	var st officeJSON
	_, body := call(t, c, "GET", srv.URL+"/api/settings/headquarters", "")
	if err := json.Unmarshal([]byte(body), &st); err != nil || st.Installed || st.Connected || !st.CatalogWritable {
		t.Errorf("durum: %s", body)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/catalog/products", `{"code":"ekmek"}`); code != 200 {
		t.Errorf("kopukken yazma: %d", code)
	}
}

func TestOfficeBadRequestsAndAuth(t *testing.T) {
	srv, c, _ := officeServer(t, true)
	for _, body := range []string{`{}`, `{`, `{"connected":"evet"}`} {
		if code, _ := call(t, c, "POST", srv.URL+"/api/settings/headquarters", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d, istenen 400", body, code)
		}
	}
	// Without a sign-in nothing is read or changed.
	anon := client(t)
	for _, tc := range [][2]string{{"GET", "/api/settings/headquarters"}, {"POST", "/api/settings/headquarters"}, {"GET", "/api/catalog/products"}, {"POST", "/api/catalog/products"}} {
		if code, _ := call(t, anon, tc[0], srv.URL+tc[1], `{"connected":false}`); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a sign-in: %d", tc[0], tc[1], code)
		}
	}
}

// If catalog-admin is not installed the catalog requests get a 404 with an explanation.
func TestCatalogNotInstalled(t *testing.T) {
	hash, _ := app.HashPassword(password)
	auth, _ := app.NewAuth(hash, login.NewMemory())
	svc := app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)
	srv := httptest.NewServer(New(svc, auth, nil, t.TempDir(), nil, nil, nil, WithCatalog(nil, nil)))
	defer srv.Close()
	c := client(t)
	post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password})
	code, body := call(t, c, "GET", srv.URL+"/api/catalog/products", "")
	if code != http.StatusNotFound || !strings.Contains(body, "kurulu değil") {
		t.Errorf("%d %s", code, body)
	}
}
