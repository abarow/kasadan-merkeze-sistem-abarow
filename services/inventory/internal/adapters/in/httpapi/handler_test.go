// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/adapters/in/httpapi"
	"abarow.com/services/inventory/internal/adapters/out/eventproto"
	"abarow.com/services/inventory/internal/app"
)

// memStore stands in for app.Store in the test.
type memStore struct {
	products  map[string]stock.Product
	levels    map[string]map[string]money.Qty // place → product → quantity
	moves     map[string][]stock.Movement     // yer → hareketler
	transfers []*stock.Transfer
	events    []outbox.Message
}

func newMem() *memStore {
	return &memStore{products: map[string]stock.Product{}, levels: map[string]map[string]money.Qty{}, moves: map[string][]stock.Movement{}}
}

func (m *memStore) SendTransfer(ctx context.Context, loc string, mv stock.Movement, t stock.Transfer, events []outbox.Message) (bool, error) {
	if ok, _ := m.Record(ctx, loc, mv, nil); !ok {
		return false, nil
	}
	m.transfers = append(m.transfers, &t)
	m.events = append(m.events, events...)
	return true, nil
}

func (m *memStore) SaveTransfer(_ context.Context, t stock.Transfer) error {
	m.transfers = append(m.transfers, &t)
	return nil
}

func (m *memStore) ReceiveTransfer(ctx context.Context, loc string, mv stock.Movement, events []outbox.Message) (bool, error) {
	m.events = append(m.events, events...)
	if ok, _ := m.Record(ctx, loc, mv, nil); !ok {
		return false, nil
	}
	for _, t := range m.transfers {
		if t.ID == mv.Ref && t.To == loc && t.Pending() {
			t.ReceivedBy, t.ReceivedAt, t.Received = mv.By, mv.At, mv.Lines
		}
	}
	return true, nil
}

func (m *memStore) Transfer(_ context.Context, id string) (stock.Transfer, error) {
	for _, t := range m.transfers {
		if t.ID == id {
			return *t, nil
		}
	}
	return stock.Transfer{}, stock.ErrUnknownTransfer
}

func (m *memStore) Transfers(context.Context, int) ([]stock.Transfer, error) {
	var out []stock.Transfer
	for _, t := range m.transfers {
		out = append(out, *t)
	}
	return out, nil
}

func (m *memStore) Enqueue(_ context.Context, e outbox.Message) error {
	m.events = append(m.events, e)
	return nil
}

func newService(store *memStore) *app.Service {
	n := 0
	newID := func() string { n++; return fmt.Sprintf("id-%d", n) }
	stores := []stock.Location{{ID: "store-1", Name: "Mağaza 1"}, {ID: "store-2", Name: "Mağaza 2"}}
	return app.NewService(store, eventproto.Encoder{NewID: newID, Now: time.Now}, app.Config{Here: app.Warehouse, Headquarters: true, Stores: stores}, newID, nil)
}

func (m *memStore) Record(_ context.Context, loc string, mv stock.Movement, events []outbox.Message) (bool, error) {
	m.events = append(m.events, events...)
	for _, x := range m.moves[loc] {
		if x.ID == mv.ID {
			return false, nil
		}
	}
	m.moves[loc] = append(m.moves[loc], mv)
	if m.levels[loc] == nil {
		m.levels[loc] = map[string]money.Qty{}
	}
	for _, l := range mv.Lines {
		m.levels[loc][l.ProductID] += l.Qty
	}
	return true, nil
}

func (m *memStore) Products(_ context.Context, ids []string) (map[string]stock.Product, error) {
	out := map[string]stock.Product{}
	for _, id := range ids {
		if p, ok := m.products[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

func (m *memStore) Quantities(_ context.Context, loc string, ids []string) (map[string]money.Qty, error) {
	out := map[string]money.Qty{}
	for _, id := range ids {
		out[id] = m.levels[loc][id]
	}
	return out, nil
}

func (m *memStore) Overview(context.Context, string, int) ([]app.Row, error) {
	var out []app.Row
	for _, p := range m.products {
		r := app.Row{Product: p, Qty: map[string]money.Qty{}}
		for loc, lv := range m.levels {
			if q, ok := lv[p.ID]; ok {
				r.Qty[loc] = q
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *memStore) Places(context.Context) ([]string, error) {
	var out []string
	for _, l := range []string{"headquarters", "store-1", "store-2"} {
		if m.levels[l] != nil {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *memStore) ByBarcode(context.Context, string, string) (stock.Item, error) {
	return stock.Item{}, app.ErrNotFound
}

func (m *memStore) Movements(_ context.Context, loc, _ string, _ int) ([]stock.Movement, error) {
	return m.moves[loc], nil
}

func (m *memStore) SaveProduct(_ context.Context, p stock.Product) error {
	m.products[p.ID] = p
	return nil
}

func (m *memStore) EnsureProducts(context.Context, []stock.Line) error { return nil }

// people are the persons reporting forwards (by name instead of key).
var people = map[string]login.Person{
	"yonetici": {Username: "headquarters", Name: "Merkez", Role: "admin", AllStores: true},
	"report":   {Username: "report", Name: "Rapor", Role: "report", Stores: []string{"store-1"}},
}

func do(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if p, ok := people[token]; ok {
		p.SetHeaders(req.Header)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Everyone can see; a report user only their own store; only the admin and the catalog
// user enter at the headquarters warehouse.
func TestStockAPI(t *testing.T) {
	ctx := context.Background()
	store := newMem()
	svc := newService(store)
	_ = svc.ApplyProduct(ctx, stock.Product{ID: "ekmek", Name: "Ekmek", Unit: stock.Piece})
	sale := func(id string) stock.Movement {
		return stock.Movement{ID: id, Kind: stock.Sale, At: time.Now(), Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek", Qty: -1000}}}
	}
	_ = svc.ApplyStoreMovement(ctx, "store-1", sale("s1"))
	_ = svc.ApplyStoreMovement(ctx, "store-2", sale("s2"))
	srv := httptest.NewServer(httpapi.New(svc))
	defer srv.Close()

	if code, _ := do(t, srv, http.MethodGet, "/api/stock", "", ""); code != http.StatusUnauthorized {
		t.Errorf("without a sign-in: %d", code)
	}
	receipt := `{"supplier":"Un Değirmeni","ref":"IRS-1","lines":[{"product_id":"ekmek","qty_milli":50000}]}`
	if code, body := do(t, srv, http.MethodPost, "/api/stock/receipts", "yonetici", receipt); code != http.StatusCreated || !strings.Contains(body, `"by":"Merkez"`) {
		t.Fatalf("goods receipt: %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodPost, "/api/stock/receipts", "report", receipt); code != http.StatusForbidden {
		t.Errorf("report user goods receipt: %d", code)
	}
	code, body := do(t, srv, http.MethodGet, "/api/stock", "yonetici", "")
	if code != http.StatusOK || !strings.Contains(body, `"locations":["headquarters","store-1","store-2"]`) || !strings.Contains(body, `"headquarters":50000`) || !strings.Contains(body, `"can_edit":true`) {
		t.Errorf("administrator: %d %s", code, body)
	}
	code, body = do(t, srv, http.MethodGet, "/api/stock", "report", "")
	if code != http.StatusOK || !strings.Contains(body, `"locations":["headquarters","store-1"]`) || strings.Contains(body, "store-2") || !strings.Contains(body, `"can_edit":false`) {
		t.Errorf("report user: %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodGet, "/api/stock/products/ekmek/movements?location=store-2", "report", ""); code != http.StatusForbidden {
		t.Errorf("another store's movements: %d", code)
	}
	if code, body := do(t, srv, http.MethodGet, "/api/stock/products/ekmek/movements?location=store-1", "report", ""); code != http.StatusOK || !strings.Contains(body, `"qty_milli":-1000`) {
		t.Errorf("their own store's movements: %d %s", code, body)
	}
}

// Transfer: the headquarters warehouse sends to a store (the event goes down to the store); a transfer
// from store to store is forwarded through headquarters, one sent to the warehouse
// is received by the warehouse (once); a report user sees only the
// transfers of their own store and cannot send.
func TestTransferAPI(t *testing.T) {
	ctx := context.Background()
	store := newMem()
	svc := newService(store)
	_ = svc.ApplyProduct(ctx, stock.Product{ID: "ekmek", Name: "Ekmek", Unit: stock.Piece})
	srv := httptest.NewServer(httpapi.New(svc))
	defer srv.Close()

	send := `{"to":"store-1","lines":[{"product_id":"ekmek","qty_milli":10000}]}`
	if code, _ := do(t, srv, http.MethodPost, "/api/stock/transfers", "report", send); code != http.StatusForbidden {
		t.Errorf("report user sent: %d", code)
	}
	if code, body := do(t, srv, http.MethodPost, "/api/stock/transfers", "yonetici", `{"to":"store-9","lines":[{"product_id":"ekmek","qty_milli":1000}]}`); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown place: %d %s", code, body)
	}
	code, body := do(t, srv, http.MethodPost, "/api/stock/transfers", "yonetici", send)
	if code != http.StatusCreated || !strings.Contains(body, `"from":"headquarters"`) || !strings.Contains(body, `"pending":true`) {
		t.Fatalf("send: %d %s", code, body)
	}
	if q := store.levels["headquarters"]["ekmek"]; q != -10000 {
		t.Errorf("was not deducted from the warehouse: %v", q)
	}

	at := time.Now()
	toWarehouse := stock.Movement{ID: "m1", Kind: stock.TransferOut, Ref: "T-9", Counterpart: "headquarters", By: "Müdür", At: at,
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek", Qty: -3000}}}
	toStore := stock.Movement{ID: "m2", Kind: stock.TransferOut, Ref: "T-8", Counterpart: "store-1", By: "Müdür", At: at,
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek", Qty: -1000}}}
	for _, m := range []stock.Movement{toWarehouse, toStore, toStore} {
		if err := svc.ApplyStoreMovement(ctx, "store-2", m); err != nil {
			t.Fatal(err)
		}
	}
	// No place list (it is published at start); sent from the warehouse and forwarded
	// from store to store: two events, in the receiving store's subject.
	if len(store.events) != 2 || !strings.HasPrefix(store.events[0].Subject, "transfer.store-1.") || store.events[1].Subject != "transfer.store-1.T-8.sent.v1" {
		t.Errorf("events: %+v", store.events)
	}

	code, body = do(t, srv, http.MethodGet, "/api/stock/transfers", "yonetici", "")
	if code != http.StatusOK || strings.Count(body, `"pending":true`) != 3 || !strings.Contains(body, `"name":"Merkez deposu"`) {
		t.Errorf("administrator transfers: %d %s", code, body)
	}
	code, body = do(t, srv, http.MethodGet, "/api/stock/transfers", "report", "")
	if code != http.StatusOK || strings.Contains(body, "T-9") || !strings.Contains(body, "T-8") || strings.Count(body, `"pending":true`) != 2 {
		t.Errorf("report user transfers: %d %s", code, body)
	}

	code, body = do(t, srv, http.MethodPost, "/api/stock/transfers/T-9/receive", "yonetici", `{"lines":[{"product_id":"ekmek","qty_milli":2000}]}`)
	if code != http.StatusCreated || !strings.Contains(body, `"counterpart":"store-2"`) {
		t.Fatalf("the warehouse received: %d %s", code, body)
	}
	if code, _ := do(t, srv, http.MethodPost, "/api/stock/transfers/T-9/receive", "yonetici", `{}`); code != http.StatusConflict {
		t.Errorf("second time: %d", code)
	}
	if code, _ := do(t, srv, http.MethodPost, "/api/stock/transfers/T-8/receive", "yonetici", `{}`); code != http.StatusUnprocessableEntity {
		t.Errorf("the warehouse received a transfer going to a store: %d", code)
	}
	if q := store.levels["headquarters"]["ekmek"]; q != -8000 {
		t.Errorf("depoya girmedi: %v", q)
	}
	// When the store receives, the transfer is closed.
	in := stock.Movement{ID: "m3", Kind: stock.TransferIn, Ref: "T-8", Counterpart: "store-2", By: "Ayşe", At: at,
		Lines: []stock.Line{{ProductID: "ekmek", Name: "Ekmek", Qty: 1000}}}
	if err := svc.ApplyStoreMovement(ctx, "store-1", in); err != nil {
		t.Fatal(err)
	}
	if tr, _ := store.Transfer(ctx, "T-8"); tr.Pending() || tr.ReceivedBy != "Ayşe" {
		t.Errorf("the store received: %+v", tr)
	}
}
