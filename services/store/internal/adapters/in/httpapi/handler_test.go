// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
	"abarow.com/pkg/nodes"
	"abarow.com/pkg/report"
	"abarow.com/pkg/sequence"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

type fakeStore struct {
	sources map[string]domain.Source
	err     error
}

func (fakeStore) Ensure(context.Context, []string) error { return nil }

func (f fakeStore) Sources(context.Context) (map[string]domain.Source, error) {
	return f.sources, f.err
}

type fakeReports struct {
	sales    []domain.Sale
	activity map[string]domain.Activity
}

func (*fakeReports) SaveSale(context.Context, domain.Sale) error { return nil }
func (*fakeReports) SaveVoid(context.Context, domain.Void) error { return nil }

func (*fakeReports) SaveUnfinished(context.Context, ledger.Unfinished) error { return nil }

func (*fakeReports) SaveShift(context.Context, domain.Shift) error { return nil }

func (f *fakeReports) Report(_ context.Context, q ledger.Filter) (ledger.Report, error) {
	return ledger.Build(q, f.sales, nil), nil
}

func (f *fakeReports) Activity(context.Context) (map[string]domain.Activity, error) {
	return f.activity, nil
}

var istanbul, _ = time.LoadLocation("Europe/Istanbul")

const password = "mağaza-parolası"

// newHandler returns a handler with a known store password.
func newHandler(t *testing.T, svc *app.Service) http.Handler {
	t.Helper()
	hash, err := app.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := app.NewAuth(hash, login.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	return New(svc, auth, nil, t.TempDir(), nil, nil, nil)
}

// client is a client that keeps the session cookie.
func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// get signs in and sends the request.
func get(t *testing.T, h http.Handler, path string, out any) int {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	c := client(t)
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}
	resp, err := c.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestAuth(t *testing.T) {
	srv := httptest.NewServer(newHandler(t, app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul)))
	defer srv.Close()
	c := client(t)
	status := func(path string) int {
		t.Helper()
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{"/api/registers", "/api/reports/summary", "/api/auth/session"} {
		if code := status(path); code != http.StatusUnauthorized {
			t.Errorf("%s without a sign-in: %d", path, code)
		}
	}
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": "yanlış-parola"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %s", resp.Status)
	}
	resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password})
	if resp.StatusCode != http.StatusOK || len(resp.Cookies()) != 1 {
		t.Fatalf("sign-in: %s %v", resp.Status, resp.Cookies())
	}
	if ck := resp.Cookies()[0]; !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.MaxAge <= 0 {
		t.Errorf("cookie: %+v", ck)
	}
	if status("/api/auth/session") != http.StatusOK || status("/api/registers") != http.StatusOK {
		t.Error("the API is closed after sign-in")
	}
	post(t, c, srv.URL+"/api/auth/logout", nil)
	if status("/api/registers") != http.StatusUnauthorized {
		t.Error("the API is open after sign-out")
	}

	// After many wrong attempts the sign-in is locked.
	var last *http.Response
	for range 5 {
		last = post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": "yanlış-parola"})
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Errorf("lock: %s", last.Status)
	}
	// The screen files do not ask for a sign-in (there is no data in them).
	if code := status("/"); code != http.StatusOK {
		t.Errorf("screen: %d", code)
	}
}

func TestRegisters(t *testing.T) {
	regs := []nodes.Node{{ID: "register-1", Name: "Giriş"}, {ID: "register-2"}}
	store := fakeStore{sources: map[string]domain.Source{"register-1": {Seen: true, Since: time.Second, Lag: 3}}}
	reports := &fakeReports{activity: map[string]domain.Activity{
		"register-1": {Receipts: 3, LastReceiptNo: 9, LastSaleAt: time.Now(), Missing: []domain.ReceiptRange{{From: 6, To: 7}},
			MissingEvents: []sequence.Gap{{Store: "store-1", Register: "register-1", Producer: "staff", From: 4, To: 4}, {Register: "register-1", Producer: "sales", From: 10, To: 12}},
			Unfinished:    []domain.Unfinished{{SaleID: "s9", CashierID: "ayse", Total: 12537, Reason: "yazıcı yanıt vermiyor", Since: time.Now(), LastAttempt: time.Now()}},
			OpenShift:     &domain.Shift{RegisterID: "register-1", ID: 3, CashierName: "Kasiyer Ayşe", OpenedAt: time.Now()},
			LastClosed:    &domain.Shift{RegisterID: "register-1", ID: 2, OpenedAt: time.Now(), ClosedAt: time.Now(), Difference: -100}},
	}}
	var body struct {
		Registers []registerJSON `json:"registers"`
	}
	code := get(t, newHandler(t, app.NewService(store, reports, regs, istanbul)), "/api/registers", &body)
	got := body.Registers
	if code != http.StatusOK || len(got) != 2 {
		t.Fatalf("%d %+v", code, got)
	}
	if got[0].ID != "register-1" || got[0].Name != "Giriş" || got[0].Link != "connected" || got[0].PendingEvents != 3 || got[0].LastSeen == nil ||
		got[0].ReceiptCount != 3 || got[0].LastReceiptNo != 9 || got[0].LastSaleAt == nil ||
		len(got[0].MissingReceipts) != 1 || got[0].MissingReceipts[0] != (rangeJSON{From: 6, To: 7}) ||
		len(got[0].MissingEvents) != 2 || got[0].MissingEvents[0] != (gapJSON{Producer: "staff", From: 4, To: 4}) || got[0].MissingEvents[1] != (gapJSON{Producer: "sales", From: 10, To: 12}) ||
		len(got[0].UnfinishedSales) != 1 || got[0].UnfinishedSales[0].SaleID != "s9" || got[0].UnfinishedSales[0].TotalKurus != 12537 ||
		got[0].UnfinishedSales[0].Reason != "yazıcı yanıt vermiyor" ||
		got[0].OpenShift == nil || got[0].OpenShift.CashierName != "Kasiyer Ayşe" || got[0].OpenShift.ClosedAt != nil ||
		got[0].LastClosedShift == nil || got[0].LastClosedShift.ShiftID != 2 || got[0].LastClosedShift.DifferenceKurus != -100 || got[0].LastClosedShift.ClosedAt == nil {
		t.Errorf("register-1: %+v", got[0])
	}
	if got[1].Name != "register-2" || got[1].Link != "never" || got[1].LastSeen != nil || got[1].LastSaleAt != nil || got[1].MissingReceipts == nil || got[1].MissingEvents == nil || got[1].UnfinishedSales == nil ||
		got[1].OpenShift != nil || got[1].LastClosedShift != nil {
		t.Errorf("register-2: %+v", got[1])
	}
}

func TestRegistersNATSDown(t *testing.T) {
	var body struct{ Error string }
	code := get(t, newHandler(t, app.NewService(fakeStore{err: errors.New("nats: timeout")}, &fakeReports{}, nil, istanbul)), "/api/registers", &body)
	if code != http.StatusServiceUnavailable || !strings.Contains(body.Error, "nats: timeout") {
		t.Errorf("%d %q", code, body.Error)
	}
}

func TestSummaryReport(t *testing.T) {
	regs := []nodes.Node{{ID: "register-1", Name: "Giriş"}}
	reports := &fakeReports{sales: []domain.Sale{{
		ID: "s1", RegisterID: "register-1", CashierID: "ayse", ReceiptNo: 1, Total: 2400, TaxTotal: 24,
		PaidAt:   time.Date(2026, 10, 26, 7, 15, 0, 0, time.UTC), // 10:15
		Lines:    []domain.Line{{ProductID: "p3", Name: "EKMEK", Qty: 2000, UnitPrice: 1200, TaxRate: 100}},
		Payments: []domain.Payment{{Method: domain.Card, Amount: 2400}},
		Taxes:    []domain.Tax{{Rate: 100, Gross: 2400, Tax: 24}},
	}}}
	h := newHandler(t, app.NewService(fakeStore{}, reports, regs, istanbul))
	var day report.SummaryJSON
	if code := get(t, h, "/api/reports/summary?from=2026-10-26", &day); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if day.From != "2026-10-26" || day.To != "2026-10-26" || day.ReceiptCount != 1 || day.TotalKurus != 2400 || day.TaxTotalKurus != 24 ||
		len(day.Payments) != 2 || day.Payments[1] != (ledger.PaymentJSON{Method: "card", AmountKurus: 2400}) ||
		len(day.Taxes) != 1 || day.Taxes[0] != (ledger.TaxJSON{RateBp: 100, GrossKurus: 2400, TaxKurus: 24}) {
		t.Errorf("day: %+v", day)
	}
	if len(day.Registers) != 1 || day.Registers[0] != (ledger.GroupJSON{ID: "register-1", Name: "Giriş", ReceiptCount: 1, TotalKurus: 2400}) ||
		len(day.Cashiers) != 1 || day.Cashiers[0].CashierID != "ayse" {
		t.Errorf("kasalar %+v, kasiyerler %+v", day.Registers, day.Cashiers)
	}
	if len(day.Hours) != 24 || day.Hours[10] != (ledger.HourJSON{Hour: 10, ReceiptCount: 1, TotalKurus: 2400}) {
		t.Errorf("saatler %+v", day.Hours)
	}
	if len(day.Products) != 1 || day.Products[0] != (ledger.ProductJSON{ProductID: "p3", Name: "EKMEK", QtyMilli: 2000, TotalKurus: 2400}) {
		t.Errorf("products %+v", day.Products)
	}

	var bad struct{ Error string }
	if code := get(t, h, "/api/reports/summary?from=26.10.2026", &bad); code != http.StatusBadRequest || !strings.Contains(bad.Error, "dönem geçersiz") {
		t.Errorf("invalid date: %d %q", code, bad.Error)
	}
	// A day with no sales: the lists are empty arrays (not null).
	reports.sales = nil
	var empty map[string]any
	get(t, h, "/api/reports/summary?from=2026-10-27", &empty)
	for _, k := range []string{"payments", "taxes", "registers", "cashiers", "products"} {
		if _, ok := empty[k].([]any); !ok {
			t.Errorf("%s is not an empty array: %v", k, empty[k])
		}
	}
}
