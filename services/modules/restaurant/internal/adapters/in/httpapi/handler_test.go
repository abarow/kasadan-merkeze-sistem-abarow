// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/modules/restaurant/internal/adapters/in/httpapi"
	"abarow.com/services/modules/restaurant/internal/adapters/out/postgres"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

var meatball = domain.Product{ID: "kofte", Name: "Köfte", UnitPrice: 32000, TaxRate: 1000, Options: []menu.Group{
	{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az"}}}}}

type catalog struct{}

func (catalog) Menu(context.Context) ([]app.MenuItem, error) {
	return []app.MenuItem{
		{Product: meatball, Category: "Ana yemekler", ImageSHA256: "abc"},
		{Product: domain.Product{ID: "balik", Name: "Levrek", Weighed: true}, Category: "Ana yemekler"},
	}, nil
}
func (catalog) Product(_ context.Context, id string) (domain.Product, error) {
	if id == "kofte" {
		return meatball, nil
	}
	return domain.Product{}, app.ErrProductNotFound
}
func (catalog) Image(context.Context, string) (app.Image, error) {
	return app.Image{SHA256: "abc", ContentType: "image/png", Data: []byte("png")}, nil
}

type sales struct{}

func (sales) CreateSale(context.Context, string, []app.SaleLine) (string, error) { return "s1", nil }
func (sales) Status(context.Context, string) (app.SaleStatus, error)             { return app.SaleOpen, nil }

type printer struct{}

func (printer) Columns(context.Context) (int, error)                  { return 32, nil }
func (printer) Print(context.Context, string, string, []string) error { return nil }

type approver struct{}

func (approver) VoidLine(_ context.Context, c, _ string) (string, error) {
	if c == "mudur" {
		return c, nil
	}
	return "", app.ErrNotAuthorized
}
func (a approver) EditFloor(ctx context.Context, c, t string) (string, error) {
	return a.VoidLine(ctx, c, t)
}

func server(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := postgres.Open(context.Background(), pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := app.NewService(store, catalog{}, sales{}, printer{}, approver{}, func() string { return "o1" })
	srv := httptest.NewServer(httpapi.New(svc, t.TempDir()))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, who, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set(httpapi.CashierHeader, who)
	req.Header.Set(httpapi.CashierNameHeader, url.QueryEscape(strings.ToUpper(who[:1])+who[1:]))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRestaurantAPI(t *testing.T) {
	srv := server(t)
	code, body := do(t, srv, http.MethodGet, "/api/module/menu", "ali", "")
	if code != http.StatusOK || !strings.Contains(body, `"name":"Ana yemekler"`) || !strings.Contains(body, `"image":"/api/module/images/kofte?h=abc"`) ||
		strings.Contains(body, "Levrek") {
		t.Errorf("menu (no weighed products): %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodGet, "/api/module/images/kofte?h=eski", "ali", ""); code != http.StatusNotFound {
		t.Errorf("image whose digest does not match: %d", code)
	}
	floor := `{"areas":[{"id":"ic","name":"İç salon","tables":[{"id":"1","name":"Masa 1"}]}]}`
	if code, _ := do(t, srv, http.MethodPut, "/api/module/floor", "ali", floor); code != http.StatusForbidden {
		t.Errorf("floor layout without approval: %d", code)
	}
	if code, body := do(t, srv, http.MethodPut, "/api/module/floor", "mudur", floor); code != http.StatusOK || !strings.Contains(body, `"Masa 1"`) {
		t.Fatalf("floor layout: %d %s", code, body)
	}

	// The event stream opens; when a table is opened an event comes.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/module/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	lines.Scan() // the opening comment

	if code, body := do(t, srv, http.MethodPost, "/api/module/tables/1/open", "ali", ""); code != http.StatusOK || !strings.Contains(body, `"opened_by":"Ali"`) {
		t.Fatalf("masa: %d %s", code, body)
	}
	got := ""
	for lines.Scan() {
		if strings.HasPrefix(lines.Text(), "data: ") {
			got = lines.Text()
			break
		}
	}
	if !strings.Contains(got, `"type":"order"`) || !strings.Contains(got, `"table_id":"1"`) {
		t.Errorf("olay: %q (%v)", got, lines.Err())
	}

	if code, body := do(t, srv, http.MethodPost, "/api/module/orders/o1/lines", "ali", `{"id":"l-1","product_id":"kofte","qty":1}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Pişirme seçin") {
		t.Errorf("line without options: %d %s", code, body)
	}
	add := `{"id":"l-1","product_id":"kofte","qty":2,"choices":[{"group":"pisirme","options":["az"]}]}`
	if code, body := do(t, srv, http.MethodPost, "/api/module/orders/o1/lines", "ali", add); code != http.StatusOK || !strings.Contains(body, `"label":"Köfte (az)"`) || !strings.Contains(body, `"pending":1`) {
		t.Fatalf("line: %d %s", code, body)
	}
	if code, body := do(t, srv, http.MethodPost, "/api/module/orders/o1/pay", "ali", `{}`); code != http.StatusUnprocessableEntity {
		t.Errorf("payment before sending: %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodPost, "/api/module/orders/o1/send", "ali", ""); code != http.StatusOK {
		t.Errorf("send: %d", code)
	}
	if code, _ := do(t, srv, http.MethodPost, "/api/module/orders/o1/lines/l-1/void", "ali", `{"qty":1,"reason":"x"}`); code != http.StatusForbidden {
		t.Errorf("cancellation without approval: %d", code)
	}
	if code, body := do(t, srv, http.MethodPost, "/api/module/orders/o1/pay", "ali", `{"version":1}`); code != http.StatusConflict {
		t.Errorf("payment with an old version: %d %s", code, body)
	}
	if code, body := do(t, srv, http.MethodPost, "/api/module/orders/o1/pay", "ali", `{}`); code != http.StatusOK || !strings.Contains(body, `"sale_id":"s1"`) {
		t.Errorf("payment: %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodGet, "/api/module/orders/yok", "ali", ""); code != http.StatusNotFound {
		t.Errorf("olmayan adisyon: %d", code)
	}

}
