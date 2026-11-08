// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/adapters/in/httpapi"
	"abarow.com/services/inventory/internal/adapters/out/eventproto"
	"abarow.com/services/inventory/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/inventory/internal/app"
)

func shopServer(t *testing.T) (*httptest.Server, *app.Service) {
	t.Helper()
	ctx := context.Background()
	store := postgrestest.Open(t).Shop("store-1")
	n := 0
	newID := func() string { n++; return fmt.Sprintf("id-%d", n) }
	svc := app.NewService(store, eventproto.Encoder{StoreID: "store-1", NewID: newID}, app.Config{Here: "store-1", Locations: store}, newID, time.Now)
	if err := svc.ApplyProduct(ctx, stock.Product{ID: "ekmek", Name: "Ekmek 250 g", Unit: stock.Piece, Barcodes: []string{"8690000000012"}}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.New(svc))
	t.Cleanup(srv.Close)
	return srv, svc
}

func shopDo(t *testing.T, method, u, person, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if person != "" {
		login.Person{Username: "ali", Name: person, Role: "manager", AllStores: true}.SetHeaders(req.Header)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestShopStockAPI(t *testing.T) {
	srv, _ := shopServer(t)
	if code, _ := shopDo(t, http.MethodGet, srv.URL+"/api/stock", "", ""); code != http.StatusUnauthorized {
		t.Errorf("request with no person: %d", code)
	}
	code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/receipts", "Müdür Ali", `{"supplier":"Un Değirmeni","ref":"IRS-1","lines":[{"product_id":"ekmek","qty_milli":20000}]}`)
	if code != http.StatusCreated || !strings.Contains(body, `"by":"Müdür Ali"`) {
		t.Fatalf("goods receipt: %d %s", code, body)
	}
	if code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/receipts", "Müdür Ali", `{"supplier":"Un Değirmeni","ref":"IRS-1","lines":[{"product_id":"ekmek","qty_milli":1000}]}`); code != http.StatusConflict {
		t.Errorf("same delivery note: %d %s", code, body)
	}
	if code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/receipts", "Müdür Ali", `{"supplier":"T","ref":"2","lines":[{"product_id":"ekmek","qty_milli":500}]}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "tam sayı") {
		t.Errorf("half a unit: %d %s", code, body)
	}
	if code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/counts", "Müdür Ali", `{"ref":"SAYIM-1","lines":[{"product_id":"ekmek","counted_milli":18000}]}`); code != http.StatusCreated || !strings.Contains(body, `"qty_milli":-2000`) {
		t.Errorf("count: %d %s", code, body)
	}
	code, body = shopDo(t, http.MethodGet, srv.URL+"/api/stock/barcode/8690000000012", "Müdür Ali", "")
	var it struct {
		Name     string `json:"name"`
		QtyMilli int64  `json:"qty_milli"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &it) != nil || it.Name != "Ekmek 250 g" || it.QtyMilli != 18000 {
		t.Errorf("barcode: %d %s", code, body)
	}
	if code, _ := shopDo(t, http.MethodGet, srv.URL+"/api/stock/barcode/000", "Müdür Ali", ""); code != http.StatusNotFound {
		t.Errorf("unknown barcode: %d", code)
	}
	code, body = shopDo(t, http.MethodGet, srv.URL+"/api/stock/products/ekmek/movements", "Müdür Ali", "")
	if code != http.StatusOK || strings.Count(body, `"kind"`) != 2 || !strings.Contains(body, `"kind":"count"`) {
		t.Errorf("hareketler: %d %s", code, body)
	}
	code, body = shopDo(t, http.MethodGet, srv.URL+"/api/stock?q="+url.QueryEscape("EKMEK"), "Müdür Ali", "")
	if code != http.StatusOK || !strings.Contains(body, `"qty_milli":{"store-1":18000}`) {
		t.Errorf("stock list: %d %s", code, body)
	}
}

// Transfer: the places, sending, receiving an incoming transfer (once).
func TestShopTransferAPI(t *testing.T) {
	ctx := context.Background()
	srv, svc := shopServer(t)
	if err := svc.ApplyLocations(ctx, []stock.Location{{ID: "headquarters", Name: "Merkez deposu"}, {ID: "store-1", Name: "Mağaza 1"}, {ID: "store-2", Name: "Mağaza 2"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyTransferSent(ctx, stock.Transfer{ID: "T-1", From: "headquarters", To: "store-1", By: "depo", At: time.Now(),
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek 250 g", Qty: 6000}}}); err != nil {
		t.Fatal(err)
	}
	code, body := shopDo(t, http.MethodGet, srv.URL+"/api/stock/locations", "Müdür Ali", "")
	if code != http.StatusOK || strings.Contains(body, `"id":"store-1"`) || !strings.Contains(body, `"Mağaza 2"`) {
		t.Errorf("yerler: %d %s", code, body)
	}
	if code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/transfers", "Müdür Ali", `{"to":"store-9","lines":[{"product_id":"ekmek","qty_milli":1000}]}`); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown place: %d %s", code, body)
	}
	if code, body := shopDo(t, http.MethodPost, srv.URL+"/api/stock/transfers", "Müdür Ali", `{"to":"store-2","lines":[{"product_id":"ekmek","qty_milli":2000}]}`); code != http.StatusCreated || !strings.Contains(body, `"to":"store-2"`) {
		t.Errorf("send: %d %s", code, body)
	}
	code, body = shopDo(t, http.MethodPost, srv.URL+"/api/stock/transfers/T-1/receive", "Müdür Ali", `{"lines":[{"product_id":"ekmek","qty_milli":5000}]}`)
	if code != http.StatusCreated || !strings.Contains(body, `"counterpart":"headquarters"`) || !strings.Contains(body, `"qty_milli":5000`) {
		t.Fatalf("teslim al: %d %s", code, body)
	}
	if code, _ := shopDo(t, http.MethodPost, srv.URL+"/api/stock/transfers/T-1/receive", "Müdür Ali", `{}`); code != http.StatusConflict {
		t.Errorf("second time: %d", code)
	}
	if code, _ := shopDo(t, http.MethodPost, srv.URL+"/api/stock/transfers/T-9/receive", "Müdür Ali", `{}`); code != http.StatusNotFound {
		t.Errorf("unknown transfer: %d", code)
	}
	code, body = shopDo(t, http.MethodGet, srv.URL+"/api/stock/transfers", "Müdür Ali", "")
	if code != http.StatusOK || !strings.Contains(body, `"here":"store-1"`) || !strings.Contains(body, `"received_milli":5000`) || strings.Count(body, `"pending"`) != 2 {
		t.Errorf("transferler: %d %s", code, body)
	}
	// The 2 sent was deducted, the 5 received was added.
	if code, body := shopDo(t, http.MethodGet, srv.URL+"/api/stock/barcode/8690000000012", "Müdür Ali", ""); code != http.StatusOK || !strings.Contains(body, `"qty_milli":3000`) {
		t.Errorf("stock: %d %s", code, body)
	}
}
