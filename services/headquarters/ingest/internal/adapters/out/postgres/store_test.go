// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"abarow.com/pkg/delivery"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// testStore opens an empty database for each test. The tests use the server in
// ABAROW_TEST_POSTGRES; they are skipped if it is not set:
//
//	docker run -d --name abarow-pgtest -e POSTGRES_PASSWORD=trial -p 127.0.0.1:15432:5432 postgres:18-alpine
//	ABAROW_TEST_POSTGRES=postgres://postgres:trial@127.0.0.1:15432/postgres?sslmode=disable go test ./...
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("ABAROW_TEST_POSTGRES is not set; the PostgreSQL tests are skipped")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abarow_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	connStr := stdlib.RegisterConnConfig(cfg)
	store, err := Open(ctx, connStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		stdlib.UnregisterConnConfig(connStr)
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("could not drop the test database: %v", err)
		}
		admin.Close()
	})
	return store
}

func queryInt(t *testing.T, s *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var at = time.Date(2026, 10, 27, 9, 30, 0, 0, time.UTC)

func saleEvent(id, store, register string) domain.Event {
	return domain.Event{ID: id, Subject: "sale.completed.v1", StoreID: store, RegisterID: register, OccurredAt: at,
		ContentType: "application/protobuf", Payload: []byte{1, 2, 3}}
}

func sale(id, store, register string, receipt int64) domain.Sale {
	return domain.Sale{
		ID: id, StoreID: store, RegisterID: register, CashierID: "ayse", ReceiptNo: receipt, FiscalNo: "SIM-1",
		Total: 13638, TaxTotal: 135, Change: 362, PaidAt: at,
		Lines: []domain.Line{
			{ProductID: "p2", Barcode: "2700001012500", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100},
			{ProductID: "p1", Barcode: "8690000000001", Name: "Ekmek", Qty: 2000, UnitPrice: 1200, TaxRate: 100},
		},
		Payments: []domain.Payment{{Method: domain.Cash, Amount: 14000}},
		Taxes:    []domain.Tax{{Rate: 100, Gross: 13638, Tax: 135}},
	}
}

func TestRecordSale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1))
	if err != nil || !fresh {
		t.Fatalf("first record: %v, %v", fresh, err)
	}
	// If the same event comes again nothing is written.
	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || fresh {
		t.Errorf("event that came again: %v, %v", fresh, err)
	}
	// If the same sale comes with another event the event is stored, the sale is not duplicated.
	if fresh, err := s.Record(ctx, saleEvent("e2", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || !fresh {
		t.Errorf("the same sale: %v, %v", fresh, err)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM sales"); n != 1 {
		t.Errorf("%d sales; 1 expected", n)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM sale_lines WHERE sale_id = 's1'"); n != 2 {
		t.Errorf("%d lines; 2 expected", n)
	}
	// The line amount is the same as the calculation at the register: 1.250 kg × 89.90 = 112.38 TL; 2 × 12.00 = 24.00 TL.
	if n := queryInt(t, s, "SELECT sum(total) FROM sale_lines"); n != 11238+2400 {
		t.Errorf("line total %d kuruş", n)
	}
	if n := queryInt(t, s, "SELECT sum(amount) FROM sale_payments"); n != 14000 {
		t.Errorf("payment total %d kuruş", n)
	}
	if n := queryInt(t, s, "SELECT sum(tax) FROM sale_taxes"); n != 135 {
		t.Errorf("VAT total %d kuruş", n)
	}

	// Registers with the same name in different stores and the same sale id are not mixed up.
	if _, err := s.Record(ctx, saleEvent("e3", "store-2", "register-1"), sale("s1", "store-2", "register-1", 1)); err != nil {
		t.Fatal(err)
	}
	activity, err := s.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a := activity["store-1"]; a.Events != 2 || a.Sales != 1 || !a.LastEventAt.Equal(at) || len(a.Registers) != 1 ||
		a.Registers[0] != (domain.RegisterActivity{ID: "register-1", Events: 2, Sales: 1, LastEventAt: at}) {
		t.Errorf("store-1: %+v", a)
	}
	if a := activity["store-2"]; a.Events != 1 || a.Sales != 1 || len(a.Registers) != 1 || a.Registers[0].Events != 1 {
		t.Errorf("store-2: %+v", a)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM sales WHERE register_id = 'register-1'"); n != 2 {
		t.Errorf("%d sales; 2 expected", n)
	}
}

func TestRecordOthers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ev := func(id, subject string) domain.Event {
		return domain.Event{ID: id, Subject: subject, StoreID: "store-1", RegisterID: "register-1", OccurredAt: at}
	}
	record := func(e domain.Event, body any) {
		t.Helper()
		if fresh, err := s.Record(ctx, e, body); err != nil || !fresh {
			t.Fatalf("%s: %v, %v", e.ID, fresh, err)
		}
	}

	record(ev("v1", "sale.voided.v1"), domain.Void{SaleID: "s9", StoreID: "store-1", RegisterID: "register-1", CashierID: "mert", Total: 999, Refunded: true, At: at})
	if n := queryInt(t, s, "SELECT count(*) FROM voids WHERE refunded AND total = 999"); n != 1 {
		t.Errorf("%d voids", n)
	}

	// A sale whose receipt could not be issued: the first time stays, the reason and the last attempt are
	// those of the last attempt.
	u := domain.Unfinished{SaleID: "s10", StoreID: "store-1", RegisterID: "register-1", CashierID: "mert", Total: 500, Reason: "yazıcı kapalı", At: at}
	record(ev("u1", "sale.charged.v1"), u)
	u.Reason, u.At = "kâğıt bitti", at.Add(time.Minute)
	record(ev("u2", "sale.charged.v1"), u)
	var reason string
	var since, last time.Time
	if err := s.db.QueryRowContext(ctx, "SELECT reason, since, last_attempt FROM unfinished WHERE sale_id = 's10'").Scan(&reason, &since, &last); err != nil {
		t.Fatal(err)
	}
	if reason != "kâğıt bitti" || !since.Equal(at) || !last.Equal(at.Add(time.Minute)) {
		t.Errorf("sale whose receipt could not be issued: %q %v %v", reason, since, last)
	}

	// Shift: the result is the same even if the closing comes before the opening.
	open := domain.Shift{StoreID: "store-1", RegisterID: "register-1", ID: 7, CashierID: "ayse", CashierName: "Ayşe", OpenedAt: at, OpeningCash: 50000}
	closed := open
	closed.ClosedAt, closed.ClosedBy, closed.Receipts, closed.Total = at.Add(9*time.Hour), "mert", 31, 120000
	closed.ExpectedCash, closed.CountedCash, closed.Difference = 150000, 149000, -1000
	record(ev("sh2", "shift.closed.v1"), closed)
	record(ev("sh1", "shift.opened.v1"), open)
	var receipts, diff int64
	var closedAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, "SELECT receipts, difference, closed_at FROM shifts WHERE shift_id = 7").Scan(&receipts, &diff, &closedAt); err != nil {
		t.Fatal(err)
	}
	if receipts != 31 || diff != -1000 || !closedAt.Valid || !closedAt.Time.Equal(closed.ClosedAt) {
		t.Errorf("shift: %d %d %v", receipts, diff, closedAt)
	}

	// An unknown event is stored only in its raw form; an event with no register does not enter the register summary.
	record(domain.Event{ID: "x1", Subject: "store.note.v1", StoreID: "store-1", OccurredAt: at.Add(time.Hour)}, nil)
	activity, err := s.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a := activity["store-1"]; a.Events != 6 || a.Sales != 0 || !a.LastEventAt.Equal(at.Add(time.Hour)) ||
		len(a.Registers) != 1 || a.Registers[0].Events != 5 || !a.Registers[0].LastEventAt.Equal(at) {
		t.Errorf("summary: %+v", a)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM events WHERE payload = ''::bytea"); n != 6 {
		t.Errorf("%d events with an empty body", n)
	}
}

// No half-finished record: if the projection cannot be written the event is not written either and it is
// processed from the start when it comes again.
func TestRecordAtomic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bad := sale("s1", "store-1", "register-1", 1)
	bad.Lines = append(bad.Lines, bad.Lines[0])
	bad.Taxes = append(bad.Taxes, bad.Taxes[0]) // the same rate twice: primary key violation
	if _, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), bad); err == nil {
		t.Fatal("a broken sale was saved")
	}
	for _, table := range []string{"events", "sales", "sale_lines", "store_stats", "registers"} {
		if n := queryInt(t, s, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || !fresh {
		t.Errorf("corrected event: %v, %v", fresh, err)
	}
}

func TestSaveLinks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	links := []domain.StoreStatus{
		{ID: "store-1", Name: "Kadıköy", Link: domain.LinkConnected, LastSeen: at, Pending: 3},
		{ID: "store-2", Name: "store-2", Link: domain.LinkNever},
	}
	if err := s.SaveLinks(ctx, links, at); err != nil {
		t.Fatal(err)
	}
	// When a store is removed from the list its state is deleted; the other's state is updated.
	links = []domain.StoreStatus{{ID: "store-1", Name: "Kadıköy", Link: domain.LinkDisconnected, LastSeen: at, Error: "stream not found"}}
	if err := s.SaveLinks(ctx, links, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var id, link, errText string
	var pending int64
	var lastSeen sql.NullTime
	var checked time.Time
	if err := s.db.QueryRowContext(ctx, "SELECT store_id, link, last_seen, pending_events, error, checked_at FROM store_links").
		Scan(&id, &link, &lastSeen, &pending, &errText, &checked); err != nil {
		t.Fatal(err)
	}
	if id != "store-1" || link != "disconnected" || !lastSeen.Valid || !lastSeen.Time.Equal(at) || pending != 0 ||
		errText != "stream not found" || !checked.Equal(at.Add(time.Minute)) {
		t.Errorf("state: %s %s %v %d %q %v", id, link, lastSeen, pending, errText, checked)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM store_links"); n != 1 {
		t.Errorf("%d store states; 1 expected", n)
	}
}

// The latest state of the store price is kept; an old state arriving out of order does not overwrite the newer.
func TestStorePrices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ev := func(id string, when time.Time) domain.Event {
		return domain.Event{ID: id, Subject: "store.price.changed.v1", StoreID: "store-1", OccurredAt: when}
	}
	monday := at.Add(72 * time.Hour)
	newer := domain.StorePrices{StoreID: "store-1", ProductID: "ekmek", ProductName: "Ekmek", UpdatedBy: "mağaza", At: at.Add(time.Minute),
		Prices: []domain.StorePrice{{Amount: 1800}, {From: monday, Clear: true}}}
	older := domain.StorePrices{StoreID: "store-1", ProductID: "ekmek", ProductName: "Ekmek", UpdatedBy: "mağaza", At: at,
		Prices: []domain.StorePrice{{Amount: 1700}}}
	for i, sp := range []domain.StorePrices{newer, older} {
		if fresh, err := s.Record(ctx, ev(fmt.Sprintf("sp%d", i), sp.At), sp); err != nil || !fresh {
			t.Fatalf("%d: %v %v", i, fresh, err)
		}
	}
	var prices string
	var updated time.Time
	if err := s.db.QueryRowContext(ctx, "SELECT prices::text, updated_at FROM store_prices WHERE store_id = 'store-1' AND product_id = 'ekmek'").
		Scan(&prices, &updated); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prices, `"amount": 1800`) || !strings.Contains(prices, `"clear": true`) || !updated.Equal(at.Add(time.Minute)) {
		t.Errorf("last state %s %v", prices, updated)
	}
	// An event with no register does not enter the register summary.
	if n := queryInt(t, s, "SELECT count(*) FROM registers"); n != 0 {
		t.Errorf("%d register summaries", n)
	}
}

// The latest state of the cashier is kept; an old state arriving out of order does not overwrite the newer.
func TestCashiers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	newer := domain.Cashier{StoreID: "store-1", CashierID: "zeynep", Name: "Zeynep Kaya", Role: "manager", Disabled: true, UpdatedBy: "mağaza", At: at.Add(time.Minute)}
	older := domain.Cashier{StoreID: "store-1", CashierID: "zeynep", Name: "Zeynep Demir", Role: "cashier", UpdatedBy: "mağaza", At: at}
	other := domain.Cashier{StoreID: "store-2", CashierID: "zeynep", Name: "Zeynep Ak", Role: "cashier", UpdatedBy: "mağaza", At: at}
	for i, c := range []domain.Cashier{newer, older, other} {
		ev := domain.Event{ID: fmt.Sprintf("c%d", i), Subject: "store.cashier.changed.v1", StoreID: c.StoreID, OccurredAt: c.At}
		if fresh, err := s.Record(ctx, ev, c); err != nil || !fresh {
			t.Fatalf("%d: %v %v", i, fresh, err)
		}
	}
	var name, role string
	var disabled bool
	if err := s.db.QueryRowContext(ctx, "SELECT name, role, disabled FROM cashiers WHERE store_id = 'store-1' AND cashier_id = 'zeynep'").
		Scan(&name, &role, &disabled); err != nil {
		t.Fatal(err)
	}
	if name != "Zeynep Kaya" || role != "manager" || !disabled {
		t.Errorf("last state %s %s %v", name, role, disabled)
	}
	if n := queryInt(t, s, "SELECT count(*) FROM cashiers"); n != 2 {
		t.Errorf("%d cashiers; one per store expected (2)", n)
	}
}

// Receipt: in the transaction the event is processed into the database a UUIDv7 receipt id is given and
// the receipt is written to the outbox to be published to the store.
func TestRecordIssuesReceipt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := 0
	s.NewID = func() string { n++; return fmt.Sprintf("receipt-%d", n) }

	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || !fresh {
		t.Fatalf("first record: %v, %v", fresh, err)
	}
	pending, err := s.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("outbox: %v, %v; one receipt expected", pending, err)
	}
	m := pending[0]
	if m.ID != "receipt-1" || m.Type != delivery.ReceiptType || m.Subject != "receipt.store-1.abarow_store" {
		t.Errorf("receipt message = %+v", m.Message)
	}
	r, err := delivery.ParseReceipt(m.Payload)
	if err != nil || r.EventID != "e1" || r.ReceiptID != "receipt-1" {
		t.Errorf("receipt content = %+v, %v", r, err)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, "SELECT receipt_id FROM delivery_issued WHERE event_id = 'e1'").Scan(&stored); err != nil || stored != "receipt-1" {
		t.Errorf("receipt in the database = %q, %v", stored, err)
	}

	// If the same event comes again the same receipt remains, a second receipt is not published.
	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || fresh {
		t.Fatalf("event that came again: %v, %v", fresh, err)
	}
	if got := queryInt(t, s, "SELECT count(*) FROM receipt_outbox"); got != 1 {
		t.Errorf("%d receipts in the outbox; none must be written for an event that came again", got)
	}

	// An event processed before this version (it has no receipt) is given its receipt when it comes again.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM delivery_issued WHERE event_id = 'e1'; DELETE FROM receipt_outbox"); err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), sale("s1", "store-1", "register-1", 1)); err != nil || fresh {
		t.Fatalf("old event again: %v, %v", fresh, err)
	}
	if got := queryInt(t, s, "SELECT count(*) FROM receipt_outbox"); got != 1 {
		t.Errorf("%d receipts for the old event; 1 expected", got)
	}

	// A published receipt drops out of the pending ones.
	pending, _ = s.Pending(ctx, 10)
	if err := s.MarkPublished(ctx, pending[0].Seq, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CountPending(ctx); got != 0 {
		t.Errorf("%d receipts pending", got)
	}
}

// An event that cannot be written has no receipt either: the transaction is rolled back.
func TestRecordRollsBackReceipt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	bad := sale("s1", "store-1", "register-1", 1)
	bad.Taxes = append(bad.Taxes, bad.Taxes[0])
	if _, err := s.Record(ctx, saleEvent("e1", "store-1", "register-1"), bad); err == nil {
		t.Fatal("a broken sale was saved")
	}
	for _, table := range []string{"delivery_issued", "receipt_outbox"} {
		if got := queryInt(t, s, "SELECT count(*) FROM "+table); got != 0 {
			t.Errorf("%s: %d rows left", table, got)
		}
	}
}
