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
	"reflect"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/pkg/report"
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

type reports struct{}

func (reports) Stores(context.Context, time.Time, time.Time) ([]domain.StoreOverview, error) {
	return []domain.StoreOverview{{ID: "store-1", Name: "Kadıköy", Link: domain.LinkConnected, CheckedAt: time.Now(), TodayReceipts: 2, TodayTotal: 18638}}, nil
}

func (reports) StorePrices(context.Context, string) ([]domain.StorePriceRow, error) {
	return []domain.StorePriceRow{{StoreID: "store-1", StoreName: "Kadıköy", ProductID: "ekmek", ProductName: "Ekmek",
		HeadquartersPrices: []domain.PricePoint{{Amount: 1500}}, StorePrices: []domain.PricePoint{{Amount: 1750}}, UpdatedBy: "mağaza"}}, nil
}

func (reports) Report(context.Context, report.Filter) (report.Result, error) {
	return report.Result{
		Report: ledger.Report{Receipts: 2, Total: 18638, Payments: []ledger.PaymentTotal{{Method: "cash", Amount: 13638}, {Method: "card", Amount: 5000}},
			Stores: []ledger.GroupTotal{{ID: "store-1", Receipts: 2, Total: 18638}}},
		StoreNames: map[string]string{"store-1": "Kadıköy"},
	}, nil
}

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	auth, err := app.NewAuth(personneltest.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AddUser(context.Background(), app.Installer, "ayse", "headquarters-test-1", app.UserInput{Name: "Ayşe Yılmaz", Role: domain.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>headquarters"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(app.NewService(reports{}, loc), report.New(reports{}, loc), auth, nil, web, nil, nil)
}

func do(h http.Handler, method, path, body string, cookie *http.Cookie, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthFlow(t *testing.T) {
	h := newHandler(t)
	// Requests without a sign-in are rejected; the screen files are open.
	for _, path := range []string{"/api/stores", "/api/reports/summary", "/api/auth/session"} {
		if rec := do(h, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a sign-in: %d", path, rec.Code)
		}
	}
	// The sign-in methods are asked without a sign-in (the sign-in screen is drawn).
	if rec := do(h, http.MethodGet, "/api/auth/methods", "", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"methods":[{"id":"password","kind":"form"`) ||
		!strings.Contains(rec.Body.String(), `"name":"username"`) {
		t.Errorf("sign-in methods: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/raporlar", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "headquarters") {
		t.Errorf("ekran: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/", "", nil); !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("security headers: %v", rec.Header())
	}

	if rec := do(h, http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"yanlış-parola"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
	// A non-JSON request (a form on another site) is rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"ayse","password":"headquarters-test-1"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON sign-in: %d", rec.Code)
	}

	rec = do(h, http.MethodPost, "/api/auth/login", `{"username":"Ayse","password":"headquarters-test-1"}`, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Ayşe Yılmaz") {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != cookieName || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure {
		t.Errorf("cookie %+v", cookie)
	}
	// Behind a reverse proxy with TLS the cookie goes only over HTTPS.
	rec = do(h, http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"headquarters-test-1"}`, nil, "X-Forwarded-Proto", "https")
	if c := rec.Result().Cookies(); len(c) != 1 || !c[0].Secure {
		t.Errorf("HTTPS cookie %+v", c)
	}

	if rec := do(h, http.MethodGet, "/api/auth/session", "", cookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"ayse"`) {
		t.Errorf("oturum: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, http.MethodGet, "/api/stores", "", cookie)
	var stores struct {
		Today  string      `json:"today"`
		Stores []storeJSON `json:"stores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stores); err != nil || rec.Code != http.StatusOK || stores.Today == "" ||
		len(stores.Stores) != 1 || stores.Stores[0].Link != "connected" || stores.Stores[0].TodayTotal != 18638 {
		t.Errorf("stores: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the report response can be cached")
	}

	rec = do(h, http.MethodGet, "/api/reports/summary?from=2026-10-01&to=2026-10-03&store=store-1", "", cookie)
	var sum report.SummaryJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil || rec.Code != http.StatusOK || sum.From != "2026-10-01" || sum.To != "2026-10-03" ||
		sum.Store != "store-1" || sum.TotalKurus != 18638 || len(sum.Days) != 3 || len(sum.Payments) != 2 || sum.Taxes == nil || sum.Cashiers == nil {
		t.Errorf("summary: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/reports/summary?from=2026-10-03&to=2026-10-01", "", cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("reversed period: %d", rec.Code)
	}

	if rec := do(h, http.MethodPost, "/api/auth/logout", "{}", cookie); rec.Code != http.StatusNoContent {
		t.Errorf("sign-out: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/stores", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("after sign-out: %d", rec.Code)
	}
}

// The catalog and stock requests are forwarded with the session checked here; the person goes
// in headers (pkg/login), a fake header put by the client is deleted.
func TestCatalogProxy(t *testing.T) {
	var got login.Person
	var gotPath string
	var forwarded bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		got, forwarded = login.PersonFrom(r.Header)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer backend.Close()
	u, _ := url.Parse(backend.URL)
	auth, _ := app.NewAuth(personneltest.New())
	if _, err := auth.AddUser(context.Background(), app.Installer, "report", "headquarters-test-2", app.UserInput{Name: "Rapor Kullanıcısı",
		Role: domain.RoleReport, Stores: []string{"store-1"}}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	h := New(app.NewService(reports{}, loc), report.New(reports{}, loc), auth, nil, t.TempDir(), u, u)

	// A request without a session is not forwarded; a fake person header is of no use.
	rec := do(h, http.MethodGet, "/api/catalog/products", "", nil, login.UserHeader, "headquarters", login.RoleHeader, "admin")
	if rec.Code != http.StatusUnauthorized || gotPath != "" {
		t.Errorf("oturumsuz: %d %q", rec.Code, gotPath)
	}
	rec = do(h, http.MethodPost, "/api/auth/login", `{"username":"report","password":"headquarters-test-2"}`, nil)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("sign-in: %d", rec.Code)
	}
	want := login.Person{Username: "report", Name: "Rapor Kullanıcısı", Role: "report", Stores: []string{"store-1"}}
	rec = do(h, http.MethodGet, "/api/catalog/products?q=ekmek", "", cookie, login.RoleHeader, "admin", login.StoresHeader, "*")
	if rec.Code != http.StatusTeapot || gotPath != "/api/catalog/products" || !forwarded || !reflect.DeepEqual(got, want) {
		t.Errorf("iletilen istek: %d %s %+v", rec.Code, gotPath, got)
	}
	// The stock requests go to inventory with the person too.
	got = login.Person{}
	rec = do(h, http.MethodGet, "/api/stock?q=ekmek", "", cookie)
	if rec.Code != http.StatusTeapot || gotPath != "/api/stock" || !reflect.DeepEqual(got, want) {
		t.Errorf("forwarded stock request: %d %s %+v", rec.Code, gotPath, got)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("no security headers")
	}
	// If catalog management is not installed (to a signed-in user) a clear error.
	bare := newHandler(t)
	rec = do(bare, http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"headquarters-test-1"}`, nil)
	cookie = rec.Result().Cookies()[0]
	rec = do(bare, http.MethodGet, "/api/catalog/products", "", cookie)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "kurulu değil") {
		t.Errorf("not installed: %d %s", rec.Code, rec.Body)
	}
	rec = do(bare, http.MethodGet, "/api/stock", "", cookie)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "stok modülü kurulu değil") {
		t.Errorf("stock not installed: %d %s", rec.Code, rec.Body)
	}
}

// The update endpoints are forwarded with the session: the registration server's admin endpoints and
// the headquarters agent's status. reporting puts the person (with their role), a fake header coming from
// the screen is deleted; if not installed a clear error.
func TestUpdateProxy(t *testing.T) {
	type call struct {
		path, method string
		person       login.Person
		forwarded    bool
		secret       string
	}
	var server, agent call
	record := func(c *call) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c.path, c.method = r.URL.Path, r.Method
			c.person, c.forwarded = login.PersonFrom(r.Header)
			c.secret = r.Header.Get(login.UpdateSecretHeader)
			w.WriteHeader(http.StatusTeapot)
		}
	}
	srv := httptest.NewServer(record(&server))
	defer srv.Close()
	ag := httptest.NewServer(record(&agent))
	defer ag.Close()
	srvURL, _ := url.Parse(srv.URL)
	agURL, _ := url.Parse(ag.URL)

	auth, _ := app.NewAuth(personneltest.New())
	if _, err := auth.AddUser(context.Background(), app.Installer, "yonetici", "headquarters-test-3", app.UserInput{Name: "Yönetici", Role: domain.RoleAdmin, AllStores: true}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	h := New(app.NewService(reports{}, loc), report.New(reports{}, loc), auth, nil, t.TempDir(), nil, nil, WithUpdate(srvURL, agURL), WithUpdateSecret("gizli"))

	// Oturumsuz istek iletilmez.
	if rec := do(h, http.MethodGet, "/api/update/registrations", "", nil, login.UserHeader, "yonetici", login.RoleHeader, "admin"); rec.Code != http.StatusUnauthorized || server.path != "" {
		t.Errorf("oturumsuz: %d %q", rec.Code, server.path)
	}
	rec := do(h, http.MethodPost, "/api/auth/login", `{"username":"yonetici","password":"headquarters-test-3"}`, nil)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("sign-in: %d", rec.Code)
	}

	// Admin endpoints: list and an action on a unit; a fake role header is overwritten by the real role.
	rec = do(h, http.MethodGet, "/api/update/registrations?status=pending", "", cookie, login.RoleHeader, "report")
	if rec.Code != http.StatusTeapot || server.path != "/api/update/registrations" || !server.forwarded || server.person.Username != "yonetici" || server.person.Role != "admin" {
		t.Errorf("liste: %d %+v", rec.Code, server)
	}
	rec = do(h, http.MethodPost, "/api/update/registrations/ABRW-X/approve", `{"code":"AAAA-BBBB"}`, cookie)
	if rec.Code != http.StatusTeapot || server.path != "/api/update/registrations/ABRW-X/approve" || server.method != http.MethodPost {
		t.Errorf("onay: %d %+v", rec.Code, server)
	}
	// The agent's status.
	rec = do(h, http.MethodGet, "/api/update/status", "", cookie)
	if rec.Code != http.StatusTeapot || agent.path != "/api/update/status" || agent.person.Username != "yonetici" {
		t.Errorf("ajan durumu: %d %+v", rec.Code, agent)
	}

	// Approval of the new version: the administrator forwards it, the secret key is added for the agent; the status request has no key.
	if agent.secret != "" {
		t.Errorf("the secret key was added to the status request: %q", agent.secret)
	}
	rec = do(h, http.MethodPost, "/api/update/apply", `{"seq":3}`, cookie, login.UpdateSecretHeader, "sahte")
	if rec.Code != http.StatusTeapot || agent.path != "/api/update/apply" || agent.secret != "gizli" || agent.person.Username != "yonetici" {
		t.Errorf("onay: %d %+v", rec.Code, agent)
	}
	for _, path := range []string{"/api/update/skip", "/api/update/snooze"} {
		agent = call{}
		if rec = do(h, http.MethodPost, path, `{}`, cookie); rec.Code != http.StatusTeapot || agent.path != path {
			t.Errorf("%s: %d %+v", path, rec.Code, agent)
		}
	}
	// A user who is not an administrator sees the status but cannot approve.
	if _, err := auth.AddUser(context.Background(), app.Installer, "report", "headquarters-test-9", app.UserInput{Name: "Rapor", Role: domain.RoleReport, AllStores: true}); err != nil {
		t.Fatal(err)
	}
	rec = do(h, http.MethodPost, "/api/auth/login", `{"username":"report","password":"headquarters-test-9"}`, nil)
	report := rec.Result().Cookies()[0]
	agent = call{}
	if rec = do(h, http.MethodGet, "/api/update/status", "", report); rec.Code != http.StatusTeapot {
		t.Errorf("the report user should see the status: %d", rec.Code)
	}
	for _, path := range []string{"/api/update/apply", "/api/update/skip", "/api/update/snooze"} {
		agent = call{}
		if rec = do(h, http.MethodPost, path, `{"seq":3}`, report); rec.Code != http.StatusForbidden || agent.path != "" {
			t.Errorf("report role %s: %d, reached the agent: %q", path, rec.Code, agent.path)
		}
	}

	// If not installed (to a signed-in user) a clear error.
	bare := newHandler(t)
	rec = do(bare, http.MethodPost, "/api/auth/login", `{"username":"ayse","password":"headquarters-test-1"}`, nil)
	cookie = rec.Result().Cookies()[0]
	for _, path := range []string{"/api/update/registrations", "/api/update/status"} {
		rec = do(bare, http.MethodGet, path, "", cookie)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "güncelleme kurulu değil") {
			t.Errorf("%s not installed: %d %s", path, rec.Code, rec.Body)
		}
	}
}
