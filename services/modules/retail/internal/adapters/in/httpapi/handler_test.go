// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"abarow.com/services/modules/retail/internal/adapters/in/httpapi"
	"abarow.com/services/modules/retail/internal/adapters/out/memory"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

type catalog struct{ searchErr error }

var items = map[string]domain.Product{
	"8690000000012": {ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100},
	"2700001":       {ID: "domates", Name: "Domates", Barcode: "2700001", Unit: domain.Kg, UnitPrice: 4990, TaxRate: 100},
}

func (catalog) ByBarcode(_ context.Context, code string) (domain.Product, error) {
	if p, ok := items[code]; ok {
		return p, nil
	}
	return domain.Product{}, app.ErrProductNotFound
}

func (c catalog) Search(_ context.Context, q string, _ int) ([]domain.Product, error) {
	if c.searchErr != nil {
		return nil, c.searchErr
	}
	var out []domain.Product
	for _, p := range items {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(q)) {
			out = append(out, p)
		}
	}
	return out, nil
}

type sales struct{ err error }

func (s sales) CreateSale(context.Context, string, []domain.Line) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "sale-1", nil
}

func newServer(t *testing.T, c catalog, s sales) *httptest.Server {
	t.Helper()
	n := 0
	svc := app.NewService(memory.NewBaskets(), c, s, func() string { n++; return fmt.Sprintf("b%d", n) })
	srv := httptest.NewServer(httpapi.New(svc, ""))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Abarow-Cashier-Id", "ayse")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestBasketFlow(t *testing.T) {
	srv := newServer(t, catalog{}, sales{})

	code, b := call(t, srv, "POST", "/api/module/baskets", "")
	if code != http.StatusCreated || b["id"] != "b1" || b["status"] != "open" {
		t.Fatalf("new basket: %d %v", code, b)
	}
	if lines, ok := b["lines"].([]any); !ok || len(lines) != 0 {
		t.Fatalf("the lines of an empty basket should be an array: %v", b["lines"])
	}

	code, b = call(t, srv, "POST", "/api/module/baskets/b1/items", `{"barcode":"8690000000012"}`)
	if code != 200 || b["total_kurus"] != float64(1500) {
		t.Fatalf("adding a product: %d %v", code, b)
	}
	code, b = call(t, srv, "PATCH", "/api/module/baskets/b1/lines/0", `{"qty_milli":3000}`)
	if code != 200 || b["total_kurus"] != float64(4500) {
		t.Fatalf("changing the quantity: %d %v", code, b)
	}
	code, b = call(t, srv, "GET", "/api/module/baskets/current", "")
	if code != 200 || b["id"] != "b1" {
		t.Fatalf("basket to continue: %d %v", code, b)
	}
	code, b = call(t, srv, "GET", "/api/module/baskets/b1", "")
	if code != 200 || b["id"] != "b1" {
		t.Fatalf("reading the basket: %d %v", code, b)
	}

	code, b = call(t, srv, "POST", "/api/module/baskets/b1/checkout", "")
	if code != 200 || b["status"] != "checked_out" || b["sale_id"] != "sale-1" {
		t.Fatalf("sending to the register: %d %v", code, b)
	}
	// A product cannot be added to a closed basket.
	if code, _ := call(t, srv, "POST", "/api/module/baskets/b1/items", `{"barcode":"8690000000012"}`); code != http.StatusConflict {
		t.Errorf("adding to a closed basket: %d, want 409", code)
	}
	code, b = call(t, srv, "POST", "/api/module/baskets/b1/reopen", "")
	if code != 200 || b["status"] != "open" {
		t.Fatalf("reopening: %d %v", code, b)
	}
	code, b = call(t, srv, "DELETE", "/api/module/baskets/b1/lines/0", "")
	if code != 200 || b["total_kurus"] != float64(0) {
		t.Fatalf("deleting a line: %d %v", code, b)
	}
}

func TestBadRequests(t *testing.T) {
	srv := newServer(t, catalog{}, sales{})
	call(t, srv, "POST", "/api/module/baskets", "")
	call(t, srv, "POST", "/api/module/baskets/b1/items", `{"barcode":"8690000000012"}`)

	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"barkodsuz", "POST", "/api/module/baskets/b1/items", `{}`, 400},
		{"bozuk gövde", "POST", "/api/module/baskets/b1/items", `{`, 400},
		{"bilinmeyen barkod", "POST", "/api/module/baskets/b1/items", `{"barcode":"0000"}`, 404},
		{"bilinmeyen sepet", "GET", "/api/module/baskets/yok", "", 404},
		{"satır no sayı değil", "PATCH", "/api/module/baskets/b1/lines/x", `{"qty_milli":1000}`, 400},
		{"miktar gövdesi bozuk", "PATCH", "/api/module/baskets/b1/lines/0", `{`, 400},
		{"eksi miktar", "PATCH", "/api/module/baskets/b1/lines/0", `{"qty_milli":-1}`, 422},
		{"olmayan satır", "PATCH", "/api/module/baskets/b1/lines/9", `{"qty_milli":1000}`, 404},
		{"silmede satır no sayı değil", "DELETE", "/api/module/baskets/b1/lines/x", "", 400},
		{"olmayan satırı silme", "DELETE", "/api/module/baskets/b1/lines/9", "", 404},
		{"tartılı ürün barkodsuz", "POST", "/api/module/baskets/b1/items", `{"barcode":"2700001"}`, 422},
	}
	for _, tc := range tests {
		if code, out := call(t, srv, tc.method, tc.path, tc.body); code != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.name, code, out, tc.want)
		}
	}
	// An empty basket cannot be sent to the register.
	call(t, srv, "POST", "/api/module/baskets", "")
	if code, _ := call(t, srv, "POST", "/api/module/baskets/b2/checkout", ""); code != 422 {
		t.Errorf("empty basket: %d, want 422", code)
	}
}

func TestErrorStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"geçersiz argüman", status.Error(codes.InvalidArgument, "x"), 422},
		{"ön koşul", status.Error(codes.FailedPrecondition, "x"), 422},
		{"ulaşılamıyor", status.Error(codes.Unavailable, "x"), 503},
		{"zaman aşımı", status.Error(codes.DeadlineExceeded, "x"), 503},
		{"bilinmeyen", errors.New("x"), 502},
	}
	for _, tc := range tests {
		srv := newServer(t, catalog{}, sales{err: tc.err})
		call(t, srv, "POST", "/api/module/baskets", "")
		call(t, srv, "POST", "/api/module/baskets/b1/items", `{"barcode":"8690000000012"}`)
		if code, _ := call(t, srv, "POST", "/api/module/baskets/b1/checkout", ""); code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, code, tc.want)
		}
	}
}

func TestProducts(t *testing.T) {
	srv := newServer(t, catalog{}, sales{})
	resp, err := http.Get(srv.URL + "/api/module/products?q=ekm")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(out) != 1 || out[0]["name"] != "Ekmek" || out[0]["unit_price_kurus"] != float64(1500) {
		t.Fatalf("product search: %d %v", resp.StatusCode, out)
	}

	srv = newServer(t, catalog{searchErr: status.Error(codes.Unavailable, "katalog yok")}, sales{})
	if code, _ := call(t, srv, "GET", "/api/module/products?q=a", ""); code != 503 {
		t.Errorf("while the catalog is closed: %d, want 503", code)
	}
}
