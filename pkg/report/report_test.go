// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
	"abarow.com/pkg/money"
	"abarow.com/pkg/report"
)

var istanbul, _ = time.LoadLocation("Europe/Istanbul")

// source is a test source that builds the ledger report from in-memory sales.
type source struct {
	sales  []ledger.Sale
	stores []string // the last store filter asked for
	max    int
}

func (s *source) Report(_ context.Context, f report.Filter) (report.Result, error) {
	s.stores, s.max = f.Stores, f.MaxProducts
	var in []ledger.Sale
	for _, sale := range s.sales {
		if f.Stores == nil || slices.Contains(f.Stores, sale.StoreID) {
			in = append(in, sale)
		}
	}
	return report.Result{
		Report:       ledger.Build(ledger.Filter{Start: f.Start, End: f.End, Stores: f.Stores, Location: f.Location}, in, nil),
		StoreNames:   map[string]string{"m1": "Merkez Şube"},
		CashierNames: map[[2]string]string{{"m1", "ayse"}: "Ayşe Yılmaz"},
	}, nil
}

func sale(store, id string, total int64) ledger.Sale {
	return ledger.Sale{
		ID: id, StoreID: store, RegisterID: "register-1", CashierID: "ayse", ReceiptNo: 1, Total: money.Money(total),
		PaidAt:   time.Date(2026, 10, 26, 7, 15, 0, 0, time.UTC), // 10:15
		Payments: []ledger.Payment{{Method: "card", Amount: money.Money(total)}},
	}
}

func newService(src report.Source, opts ...report.Option) *report.Service {
	s := report.New(src, istanbul, opts...)
	s.SetClock(func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, istanbul) })
	return s
}

func TestSummaryScopes(t *testing.T) {
	src := &source{sales: []ledger.Sale{sale("m1", "a", 1000), sale("m2", "b", 500), sale("m3", "c", 200)}}
	svc := newService(src)
	ctx := context.Background()
	all := login.Person{Username: "ali", AllStores: true}
	some := login.Person{Username: "veli", Stores: []string{"m1", "m2"}}

	sum, err := svc.Summary(ctx, report.Query{}, all)
	if err != nil || src.stores != nil || sum.Receipts != 3 || sum.From != "2026-10-26" || sum.To != "2026-10-26" {
		t.Fatalf("one who sees all: %+v %v (filter %v)", sum, err, src.stores)
	}
	sum, err = svc.Summary(ctx, report.Query{}, some)
	if err != nil || !slices.Equal(src.stores, []string{"m1", "m2"}) || sum.Receipts != 2 {
		t.Fatalf("one who sees two stores: %+v %v (filter %v)", sum, err, src.stores)
	}
	sum, err = svc.Summary(ctx, report.Query{Store: "m2"}, some)
	if err != nil || !slices.Equal(src.stores, []string{"m2"}) || sum.Receipts != 1 || sum.StoreID != "m2" {
		t.Fatalf("selected store: %+v %v", sum, err)
	}
	if _, err = svc.Summary(ctx, report.Query{Store: "m3"}, some); !errors.Is(err, report.ErrForbidden) {
		t.Errorf("a store they cannot see: %v", err)
	}
	// A person with no stores sees nothing (the filter is empty, not nil).
	sum, err = svc.Summary(ctx, report.Query{}, login.Person{Username: "yok"})
	if err != nil || src.stores == nil || len(src.stores) != 0 || sum.Receipts != 0 {
		t.Errorf("person with no stores: %+v %v (filter %#v)", sum, err, src.stores)
	}
}

func TestSummarySingleStore(t *testing.T) {
	src := &source{sales: []ledger.Sale{sale("m1", "a", 1000)}}
	svc := newService(src, report.SingleStore(), report.MaxProducts(7))
	sum, err := svc.Summary(context.Background(), report.Query{Store: "baska"}, login.Person{AllStores: true})
	if err != nil || src.stores != nil || src.max != 7 || sum.StoreID != "" || sum.Receipts != 1 {
		t.Errorf("single store: %+v %v (filter %v, product limit %d)", sum, err, src.stores, src.max)
	}
}

func TestSummaryPeriod(t *testing.T) {
	svc := newService(&source{})
	all := login.Person{AllStores: true}
	sum, err := svc.Summary(context.Background(), report.Query{From: "2026-10-24", To: "2026-10-26"}, all)
	if err != nil || len(sum.Days) != 3 || sum.Days[0].Date != "2026-10-24" || sum.Days[2].Date != "2026-10-26" {
		t.Errorf("days with no sales are listed too: %+v %v", sum.Days, err)
	}
	for _, q := range []report.Query{{From: "26.10.2026"}, {From: "2026-10-26", To: "2026-10-25"}} {
		if _, err := svc.Summary(context.Background(), q, all); !errors.Is(err, report.ErrInvalidPeriod) {
			t.Errorf("%+v: %v", q, err)
		}
	}
}

func get(t *testing.T, h http.Handler, path string, p login.Person) (int, map[string]any) {
	t.Helper()
	web := login.Web{Auth: fixed{p}, Cookie: login.Cookie{Name: "c", TTL: time.Hour}}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	r.AddCookie(&http.Cookie{Name: "c", Value: "t"})
	web.Require(h.ServeHTTP)(rec, r)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v (%s)", path, err, rec.Body)
	}
	return rec.Code, out
}

// fixed is an Authenticator that resolves every key to the same person.
type fixed struct{ p login.Person }

func (fixed) Methods(context.Context) (login.Methods, error) { return login.Methods{}, nil }
func (fixed) Login(context.Context, map[string]string) (string, login.Person, error) {
	return "", login.Person{}, nil
}
func (f fixed) Session(context.Context, string) (login.Person, error) { return f.p, nil }
func (fixed) Logout(context.Context, string) error                    { return nil }

func TestHandler(t *testing.T) {
	src := &source{sales: []ledger.Sale{sale("m1", "a", 2400)}}
	all := login.Person{AllStores: true}
	t.Run("headquarters", func(t *testing.T) {
		h := newService(src).Handler()
		code, out := get(t, h, "/api/reports/summary?from=2026-10-26&store=m1", all)
		if code != 200 || out["store"] != "m1" || out["total_kurus"] != float64(2400) || out["receipt_count"] != float64(1) {
			t.Fatalf("summary: %d %v", code, out)
		}
		cashiers := out["cashiers"].([]any)
		if len(cashiers) != 1 || cashiers[0].(map[string]any)["cashier_name"] != "Ayşe Yılmaz" || cashiers[0].(map[string]any)["store_id"] != "m1" {
			t.Errorf("kasiyerler: %v", cashiers)
		}
		if stores := out["stores"].([]any); len(stores) != 1 || stores[0].(map[string]any)["name"] != "Merkez Şube" {
			t.Errorf("stores: %v", stores)
		}
		// If there is no service that names the registers the register breakdown is an empty array.
		if regs, ok := out["registers"].([]any); !ok || len(regs) != 0 {
			t.Errorf("kasalar: %v", out["registers"])
		}
		if hours := out["hours"].([]any); len(hours) != 24 {
			t.Errorf("saatler: %d", len(hours))
		}
		if code, out := get(t, h, "/api/reports/summary?from=bozuk", all); code != 400 || out["error"] == nil {
			t.Errorf("invalid period: %d %v", code, out)
		}
		if code, _ := get(t, h, "/api/reports/summary?store=m1", login.Person{Stores: []string{"m2"}}); code != 403 {
			t.Errorf("a store they cannot see: %d", code)
		}
	})
	t.Run("store", func(t *testing.T) {
		h := newService(src, report.SingleStore(), report.RegisterNames(func(id string) string { return "Giriş " + id })).Handler()
		_, out := get(t, h, "/api/reports/summary", all)
		regs := out["registers"].([]any)
		if len(regs) != 1 || regs[0].(map[string]any)["name"] != "Giriş register-1" {
			t.Errorf("kasalar: %v", regs)
		}
	})
}

func TestBackend(t *testing.T) {
	var got struct{ path, user string }
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.user = r.URL.Path, r.Header.Get(login.UserHeader)
	}))
	defer back.Close()
	target, _ := url.Parse(back.URL)
	web := login.Web{Auth: fixed{login.Person{Username: "ayse", Name: "Ayşe", AllStores: true}}, Cookie: login.Cookie{Name: "c", TTL: time.Hour}}

	mux := http.NewServeMux()
	report.Backend(mux, web, target, "modül yok", "/api/stock", "/api/stock/")
	report.Backend(mux, web, nil, "katalog yönetimi kurulu değil", "/api/catalog/")
	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "c", Value: "t"})
		r.Header.Set(login.UserHeader, "sahte")
		mux.ServeHTTP(rec, r)
		return rec
	}
	if rec := do("/api/stock/transfers"); rec.Code != 200 || got.path != "/api/stock/transfers" || got.user != "ayse" {
		t.Errorf("iletim: %d %+v", rec.Code, got)
	}
	if rec := do("/api/catalog/products"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "katalog yönetimi kurulu değil") {
		t.Errorf("module not installed: %d %s", rec.Code, rec.Body)
	}
}

func TestSecure(t *testing.T) {
	rec := httptest.NewRecorder()
	report.Secure(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("no %s", h)
		}
	}
}
