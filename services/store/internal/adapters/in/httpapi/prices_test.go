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

	"abarow.com/pkg/login"
	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/store/internal/adapters/out/priceproto"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

func TestPricesAPI(t *testing.T) {
	ctx := context.Background()
	st := postgrestest.Open(t)
	n := 0
	prices := app.NewPrices(st, st, priceproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }}, istanbul)
	if err := prices.ApplyCatalog(ctx, domain.CatalogProduct{ID: "ekmek", Name: "Ekmek 250 g", Unit: "piece", HeadquartersPrices: []domain.PricePoint{{Amount: 1500}}}); err != nil {
		t.Fatal(err)
	}
	hash, _ := app.HashPassword(password)
	auth, _ := app.NewAuth(hash, login.NewMemory())
	srv := httptest.NewServer(New(app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul), auth, nil, t.TempDir(), prices, nil, nil))
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
	if code, _ := call(http.MethodGet, "/api/prices", ""); code != http.StatusUnauthorized {
		t.Errorf("without a sign-in: %d", code)
	}
	if resp := post(t, c, srv.URL+"/api/auth/login", map[string]string{"password": password}); resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %s", resp.Status)
	}
	code, body := call(http.MethodGet, "/api/prices", "")
	if code != http.StatusOK || !strings.Contains(body, `"headquarters_price_kurus":1500`) || strings.Contains(body, "store_price_kurus") {
		t.Errorf("list: %d %s", code, body)
	}
	code, body = call(http.MethodPost, "/api/prices/ekmek", `{"unit_price_kurus":1750}`)
	if code != http.StatusOK || !strings.Contains(body, `"store_price_kurus":1750`) {
		t.Errorf("store price: %d %s", code, body)
	}
	code, body = call(http.MethodPost, "/api/prices/ekmek/clear", `{"from":"2099-11-02T00:00:00+03:00"}`)
	if code != http.StatusOK || !strings.Contains(body, `"clear":true`) {
		t.Errorf("future-dated removal: %d %s", code, body)
	}
	if code, _ := call(http.MethodPost, "/api/prices/ekmek/cancel", `{"from":"2020-01-01T00:00:00Z"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("withdrawing a past record: %d", code)
	}
	if code, _ := call(http.MethodPost, "/api/prices/yok", `{"unit_price_kurus":1}`); code != http.StatusNotFound {
		t.Errorf("non-existent product: %d", code)
	}
	code, body = call(http.MethodGet, "/api/prices/ekmek", "")
	var out struct {
		Product productPricesJSON `json:"product"`
		History []priceChangeJSON `json:"history"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != http.StatusOK || len(out.History) != 2 || out.History[0].By != app.StorePasswordName ||
		len(out.Product.StoreUpcoming) != 1 {
		t.Errorf("product: %d %s", code, body)
	}

	// On a store server not connected to headquarters the price requests are rejected with an explanation.
	h := newHandler(t, app.NewService(fakeStore{}, &fakeReports{}, nil, istanbul))
	var msg map[string]string
	if code := get(t, h, "/api/prices", &msg); code != http.StatusNotFound || !strings.Contains(msg["error"], "merkeze bağlı") {
		t.Errorf("merkezsiz: %d %v", code, msg)
	}
}
