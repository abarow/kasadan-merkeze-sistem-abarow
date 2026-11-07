// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/pkg/report"
	"abarow.com/services/store/internal/adapters/out/cashierproto"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

func TestCashiersAPI(t *testing.T) {
	st := personneltest.New()
	n := 0
	cashiers := app.NewCashiers(st, cashierproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }})
	cashiers.HashPIN = func(pin string) ([]byte, error) { return bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost) }
	hash, _ := app.HashPassword(password)
	auth, _ := app.NewAuth(hash, st)
	reports := &fakeReports{sales: []domain.Sale{{
		ID: "s1", RegisterID: "register-1", CashierID: "zeynep", ReceiptNo: 1, Total: 1500, TaxTotal: 15,
		PaidAt: time.Now(), Payments: []domain.Payment{{Method: domain.Cash, Amount: 1500}},
	}}}
	srv := httptest.NewServer(New(app.NewService(fakeStore{}, reports, nil, istanbul), auth, nil, t.TempDir(), nil, cashiers, nil))
	defer srv.Close()

	c := client(t)
	call := func(method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := call(http.MethodGet, "/api/cashiers", ""); code != http.StatusUnauthorized {
		t.Errorf("without a sign-in: %d", code)
	}
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}

	code, body := call(http.MethodPost, "/api/cashiers", `{"id":"zeynep","name":"Zeynep Demir","role":"cashier","pin":"2468"}`)
	if code != http.StatusCreated || !strings.Contains(body, `"name":"Zeynep Demir"`) || strings.Contains(strings.ToLower(body), "pin") {
		t.Fatalf("ekleme: %d %s", code, body)
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"id":"zeynep","name":"Zeynep","role":"cashier","pin":"2468"}`, http.StatusConflict},
		{`{"id":"Zeynep","name":"Zeynep","role":"cashier","pin":"2468"}`, http.StatusUnprocessableEntity},
		{`{"id":"ali","name":"Ali","role":"cashier","pin":"12"}`, http.StatusUnprocessableEntity},
		{`{"id":"ali","name":"Ali","role":"patron","pin":"1234"}`, http.StatusUnprocessableEntity},
		{`bozuk`, http.StatusBadRequest},
	} {
		if code, body := call(http.MethodPost, "/api/cashiers", tc.body); code != tc.want {
			t.Errorf("%s: %d %s", tc.body, code, body)
		}
	}
	if code, body := call(http.MethodPost, "/api/cashiers/zeynep", `{"name":"Zeynep Kaya","role":"manager"}`); code != http.StatusOK || !strings.Contains(body, `"role":"manager"`) {
		t.Errorf("update: %d %s", code, body)
	}
	if code, body := call(http.MethodPost, "/api/cashiers/zeynep/pin", `{"pin":"8642"}`); code != http.StatusOK {
		t.Errorf("PIN: %d %s", code, body)
	}
	if code, body := call(http.MethodPost, "/api/cashiers/zeynep/pin", `{"pin":"86"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("short PIN: %d %s", code, body)
	}
	if code, body := call(http.MethodPost, "/api/cashiers/zeynep/disabled", `{"disabled":true}`); code != http.StatusOK || !strings.Contains(body, `"disabled":true`) {
		t.Errorf("kapatma: %d %s", code, body)
	}
	if code, _ := call(http.MethodPost, "/api/cashiers/yok/disabled", `{"disabled":true}`); code != http.StatusNotFound {
		t.Errorf("olmayan kasiyer: %d", code)
	}
	code, body = call(http.MethodGet, "/api/cashiers/zeynep", "")
	var detail struct {
		Cashier cashierJSON         `json:"cashier"`
		History []cashierChangeJSON `json:"history"`
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil || code != http.StatusOK {
		t.Fatalf("detail: %d %s", code, body)
	}
	if !detail.Cashier.Disabled || detail.Cashier.Name != "Zeynep Kaya" || len(detail.History) != 4 || detail.History[0].Detail != "hesap kapatıldı" || detail.History[0].By != app.StorePasswordName {
		t.Errorf("detail %+v", detail)
	}
	if code, body := call(http.MethodGet, "/api/cashiers", ""); code != http.StatusOK || !strings.Contains(body, `"id":"zeynep"`) {
		t.Errorf("liste: %d %s", code, body)
	}

	// The cashier's name shows in the day report.
	_, body = call(http.MethodGet, "/api/reports/summary", "")
	var day report.SummaryJSON
	if err := json.Unmarshal([]byte(body), &day); err != nil || len(day.Cashiers) != 1 || day.Cashiers[0].CashierName != "Zeynep Kaya" {
		t.Errorf("cashier in the day report: %s", body)
	}
}

func TestNoCashiers(t *testing.T) {
	var body map[string]string
	code := get(t, newHandler(t, app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)), "/api/cashiers", &body)
	if code != http.StatusNotFound || !strings.Contains(body["error"], "ABAROW_STORE_ID") {
		t.Errorf("while there is no store id: %d %v", code, body)
	}
}

// A manager signs in to the screen with their own code and PIN; their name is written to the change
// records. They cannot close their own account or remove their own managership. While a manager
// with an open account exists the store password is off.
func TestManagerLogin(t *testing.T) {
	ctx := context.Background()
	st := personneltest.New()
	n := 0
	cashiers := app.NewCashiers(st, cashierproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }})
	cashiers.HashPIN = func(pin string) ([]byte, error) { return bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost) }
	hash, _ := app.HashPassword(password)
	auth, _ := app.NewAuth(hash, st)
	auth.AllowManagers(app.PeopleDirectory{Store: st})
	srv := httptest.NewServer(New(app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul), auth, nil, t.TempDir(), nil, cashiers, nil))
	defer srv.Close()

	call := func(c *http.Client, method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	login := func(c *http.Client, id, pin string) (int, string) {
		t.Helper()
		return call(c, http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"cashier_id":%q,"pin":%q}`, id, pin))
	}

	// While there is no manager the sign-in is with the store password; "store password" is
	// written to the record.
	if code, body := call(client(t), http.MethodGet, "/api/auth/methods", ""); code != http.StatusOK ||
		!strings.Contains(body, `"methods":[{"id":"password","kind":"form"`) {
		t.Errorf("while there is no manager: %d %s", code, body)
	}
	shop := client(t)
	if code, body := call(shop, http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"password":%q}`, password)); code != http.StatusOK || body != `{"username":"","name":"mağaza parolası","role":"manager","all_stores":true,"stores":[]}` {
		t.Fatalf("store password: %d %s", code, body)
	}
	if code, body := call(shop, http.MethodPost, "/api/cashiers", `{"id":"ilk","name":"İlk Kasiyer","role":"cashier","pin":"1111"}`); code != http.StatusCreated || !strings.Contains(body, `"updated_by":"mağaza parolası"`) {
		t.Errorf("change with the store password: %d %s", code, body)
	}
	for _, c := range []struct {
		id, name string
		role     domain.Role
		pin      string
	}{
		{"ayse", "Ayşe Yılmaz", domain.RoleManager, "1234"},
		{"mehmet", "Mehmet Er", domain.RoleManager, "5678"},
		{"ali", "Ali Kaya", domain.RoleCashier, "2468"},
	} {
		if _, err := cashiers.Add(ctx, "kurulum", c.id, c.name, c.role, c.pin, ""); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := call(client(t), http.MethodGet, "/api/auth/methods", ""); code != http.StatusOK ||
		!strings.Contains(body, `"methods":[{"id":"manager","kind":"form"`) ||
		!strings.Contains(body, `"note_id":"password-closed"`) || strings.Contains(body, `"id":"password"`) {
		t.Errorf("while there is a manager: %d %s", code, body)
	}
	// When a manager is added the store password is off: the open session closes too.
	if code, _ := call(shop, http.MethodGet, "/api/cashiers", ""); code != http.StatusUnauthorized {
		t.Errorf("session opened with the password when a manager was added: %d", code)
	}
	if code, body := call(shop, http.MethodPost, "/api/auth/login", fmt.Sprintf(`{"password":%q}`, password)); code != http.StatusForbidden || !strings.Contains(body, "mağaza parolası kapalı") {
		t.Errorf("store password while there is a manager: %d %s", code, body)
	}

	ayse := client(t)
	for _, tc := range []struct {
		id, pin string
		want    int
	}{
		{"ayse", "9999", http.StatusUnauthorized},
		{"yok", "1234", http.StatusUnauthorized},
		{"ali", "2468", http.StatusForbidden},
	} {
		if code, body := login(ayse, tc.id, tc.pin); code != tc.want {
			t.Errorf("%s/%s: %d %s", tc.id, tc.pin, code, body)
		}
	}
	if code, body := login(ayse, "ayse", "1234"); code != http.StatusOK || body != `{"username":"ayse","name":"Ayşe Yılmaz","role":"manager","all_stores":true,"stores":[]}` {
		t.Fatalf("manager sign-in: %d %s", code, body)
	}
	if code, body := call(ayse, http.MethodGet, "/api/auth/session", ""); code != http.StatusOK || !strings.Contains(body, `"name":"Ayşe Yılmaz"`) {
		t.Errorf("oturum: %d %s", code, body)
	}
	if code, body := call(ayse, http.MethodPost, "/api/cashiers/ali", `{"name":"Ali Demir","role":"cashier"}`); code != http.StatusOK || !strings.Contains(body, `"updated_by":"Ayşe Yılmaz"`) {
		t.Errorf("who made the change: %d %s", code, body)
	}
	if code, body := call(ayse, http.MethodPost, "/api/cashiers/ayse/disabled", `{"disabled":true}`); code != http.StatusConflict {
		t.Errorf("closing own account: %d %s", code, body)
	}
	if code, body := call(ayse, http.MethodPost, "/api/cashiers/ayse", `{"name":"Ayşe Yılmaz","role":"cashier"}`); code != http.StatusConflict {
		t.Errorf("removing own managership: %d %s", code, body)
	}

	// The session of the manager who changed their own PIN stays open; the other session opened
	// with the old PIN closes.
	other := client(t)
	if code, _ := login(other, "ayse", "1234"); code != http.StatusOK {
		t.Fatal("a second session was not opened")
	}
	if code, body := call(ayse, http.MethodPost, "/api/cashiers/ayse/pin", `{"pin":"4321"}`); code != http.StatusOK {
		t.Fatalf("PIN: %d %s", code, body)
	}
	if code, _ := call(ayse, http.MethodGet, "/api/auth/session", ""); code != http.StatusOK {
		t.Error("the session of the manager who changed their own PIN closed")
	}
	if code, _ := call(other, http.MethodGet, "/api/auth/session", ""); code != http.StatusUnauthorized {
		t.Error("the session opened with the old PIN stayed open")
	}

	// When another manager closes their account their session closes, they cannot sign in.
	mehmet := client(t)
	if code, _ := login(mehmet, "mehmet", "5678"); code != http.StatusOK {
		t.Fatal("the second manager could not sign in")
	}
	if code, body := call(mehmet, http.MethodPost, "/api/cashiers/ayse/disabled", `{"disabled":true}`); code != http.StatusOK || !strings.Contains(body, `"updated_by":"Mehmet Er"`) {
		t.Fatalf("hesap kapatma: %d %s", code, body)
	}
	if code, _ := call(ayse, http.MethodGet, "/api/cashiers", ""); code != http.StatusUnauthorized {
		t.Errorf("session of the closed account: %d", code)
	}
	if code, body := login(ayse, "ayse", "4321"); code != http.StatusForbidden || !strings.Contains(body, "hesabınız kapalı") {
		t.Errorf("sign-in with a closed account: %d %s", code, body)
	}
}
