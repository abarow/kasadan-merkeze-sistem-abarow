// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/outbox"
	"abarow.com/services/headquarters/catalog/internal/adapters/out/eventproto"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// admin and reporter are the people reporting passes on.
var (
	admin    = login.Person{Username: "ayse", Name: "Ayşe Yılmaz", Role: "admin", AllStores: true}
	reporter = login.Person{Username: "ali", Name: "Ali Kaya", Role: "report", Stores: []string{"store-1"}}
)

type repo struct {
	products map[string]domain.Product
	changes  []app.Change
	images   map[string]domain.Image
}

func (m *repo) Get(_ context.Context, code string) (domain.Product, error) {
	p, ok := m.products[code]
	if !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	return p, nil
}
func (m *repo) List(context.Context, string, int) ([]domain.Product, error) {
	var out []domain.Product
	for _, p := range m.products {
		out = append(out, p)
	}
	return out, nil
}
func (m *repo) Create(_ context.Context, p domain.Product, c app.Change, _ outbox.Message) error {
	if _, ok := m.products[p.Code]; ok {
		return domain.ErrExists
	}
	m.products[p.Code], m.changes = p, append(m.changes, c)
	return nil
}
func (m *repo) Update(_ context.Context, p domain.Product, c app.Change, _ outbox.Message) error {
	if m.products[p.Code].Version != p.Version-1 {
		return domain.ErrConflict
	}
	m.products[p.Code], m.changes = p, append(m.changes, c)
	return nil
}
func (m *repo) History(context.Context, string, int) ([]app.Change, error) { return m.changes, nil }
func (m *repo) SetImage(_ context.Context, p domain.Product, img domain.Image, c app.Change, _ []outbox.Message) error {
	if m.products[p.Code].Version != p.Version-1 {
		return domain.ErrConflict
	}
	m.products[p.Code], m.changes = p, append(m.changes, c)
	if m.images == nil {
		m.images = map[string]domain.Image{}
	}
	m.images[p.Code] = img
	return nil
}
func (m *repo) Image(_ context.Context, code string) (domain.Image, error) {
	if img, ok := m.images[code]; ok && len(img.Data) > 0 {
		return img, nil
	}
	return domain.Image{}, domain.ErrNotFound
}

func do(h http.Handler, method, path, body string, cookie bool, contentType ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(contentType) > 0 {
		req.Header.Set("Content-Type", contentType[0])
	}
	if cookie {
		admin.SetHeaders(req.Header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCatalogAPI(t *testing.T) {
	ist, _ := time.LoadLocation("Europe/Istanbul")
	svc := app.NewService(&repo{products: map[string]domain.Product{}}, eventproto.Encoder{NewID: func() string { return "e" }}, ist)
	h := New(svc)

	if rec := do(h, http.MethodGet, "/api/catalog/products", "", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a sign-in: %d", rec.Code)
	}
	create := `{"code":"ekmek","name":"Ekmek 250 g","unit":"piece","tax_rate_bp":100,"barcodes":["8690000000012"],"quick_key":true,"unit_price_kurus":1500}`
	if rec := do(h, http.MethodPost, "/api/catalog/products", create, true, "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON olmayan istek: %d", rec.Code)
	}
	rec := do(h, http.MethodPost, "/api/catalog/products", create, true)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"updated_by":"ayse"`) || !strings.Contains(rec.Body.String(), `"unit_price_kurus":1500`) {
		t.Fatalf("ekleme: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/catalog/products", create, true); rec.Code != http.StatusConflict {
		t.Errorf("same code: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/catalog/products", `{"code":"a.b","name":"X","unit":"piece"}`, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid product: %d", rec.Code)
	}

	// Future-dated price.
	rec = do(h, http.MethodPost, "/api/catalog/products/ekmek/prices", `{"version":1,"unit_price_kurus":1650,"from":"2099-11-02T00:00:00+03:00"}`, true)
	var p productJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != http.StatusOK || p.Version != 2 || len(p.Prices) != 2 ||
		p.UnitPriceKurus != 1500 || p.Prices[1].From == nil || p.Prices[1].From.Year() != 2099 {
		t.Fatalf("price: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/catalog/products/ekmek/prices", `{"version":1,"unit_price_kurus":1700}`, true); rec.Code != http.StatusConflict {
		t.Errorf("old version: %d", rec.Code)
	}
	cancel := `{"version":2,"from":"` + p.Prices[1].From.Format(time.RFC3339Nano) + `"}`
	if rec := do(h, http.MethodPost, "/api/catalog/products/ekmek/prices/cancel", cancel, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":3`) {
		t.Errorf("undo: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/catalog/products/ekmek/discontinue", `{"version":3,"discontinued":true}`, true); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"discontinued":true`) {
		t.Errorf("withdrawal from sale: %d %s", rec.Code, rec.Body)
	}

	rec = do(h, http.MethodGet, "/api/catalog/products/ekmek", "", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"history":[`) || !strings.Contains(rec.Body.String(), "satıştan kaldırıldı") {
		t.Errorf("product and history: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/catalog/products/yok", "", true); rec.Code != http.StatusNotFound {
		t.Errorf("non-existent product: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/catalog/products?q=ekmek", "", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code":"ekmek"`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
}

// The report user reads the catalog, cannot change it.
func TestCatalogReadOnlyRole(t *testing.T) {
	ist, _ := time.LoadLocation("Europe/Istanbul")
	svc := app.NewService(&repo{products: map[string]domain.Product{}}, eventproto.Encoder{NewID: func() string { return "e" }}, ist)
	h := New(svc)
	as := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		reporter.SetHeaders(req.Header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := as(http.MethodGet, "/api/catalog/products", ""); rec.Code != http.StatusOK {
		t.Errorf("okuma: %d %s", rec.Code, rec.Body)
	}
	create := `{"code":"ekmek","name":"Ekmek 250 g","unit":"piece","tax_rate_bp":100,"unit_price_kurus":1500}`
	if rec := as(http.MethodPost, "/api/catalog/products", create); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "yetkiniz yok") {
		t.Errorf("a report user added a product: %d %s", rec.Code, rec.Body)
	}
	if rec := as(http.MethodPost, "/api/catalog/products/ekmek/prices", `{"unit_price_kurus":1600}`); rec.Code != http.StatusForbidden {
		t.Errorf("a report user gave a price: %d", rec.Code)
	}
}

// On the store server write permission belongs to the store manager (ABAROW_CATALOG_WRITERS);
// headquarters' roles cannot write there.
func TestCatalogWriterRoles(t *testing.T) {
	ist, _ := time.LoadLocation("Europe/Istanbul")
	svc := app.NewService(&repo{products: map[string]domain.Product{}}, eventproto.Encoder{NewID: func() string { return "e" }}, ist)
	h := New(svc, "manager")
	as := func(p login.Person) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/catalog/products",
			strings.NewReader(`{"code":"ekmek","name":"Ekmek 250 g","unit":"piece","tax_rate_bp":100,"unit_price_kurus":1500}`))
		req.Header.Set("Content-Type", "application/json")
		p.SetHeaders(req.Header)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	manager := login.Person{Username: "ali", Name: "Müdür Ali", Role: "manager", AllStores: true}
	if rec := as(manager); rec.Code != http.StatusCreated {
		t.Errorf("the manager could not add a product: %d %s", rec.Code, rec.Body)
	}
	if rec := as(admin); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "manager") {
		t.Errorf("a non-manager wrote: %d %s", rec.Code, rec.Body)
	}
}
