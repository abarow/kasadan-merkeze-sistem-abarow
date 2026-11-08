// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel"
	"abarow.com/pkg/report"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// ingestMigrations are the ingest migrations that set up the sales tables;
// catalogMigrations are the catalog-admin migrations that set up headquarters' catalog.
// The tests set up the schema as the services set it up.
const (
	ingestMigrations  = "../../../../../ingest/internal/adapters/out/postgres/migrations"
	catalogMigrations = "../../../../../catalog/internal/adapters/out/postgres/migrations"
)

// testStore opens an empty database with ingest's and reporting's schema for each test.
// The tests use the server in ABAROW_TEST_POSTGRES; they are skipped if it is not set
// (see the postgres tests of services/headquarters/ingest).
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("ABAROW_TEST_POSTGRES yok; PostgreSQL testleri atlandı")
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
	db := stdlib.OpenDB(*cfg)
	// Open takes a connection string: the test database's address.
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	connStr := u.String()
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(ingestMigrations))
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err == nil {
		p, err = goose.NewProvider(goose.DialectPostgres, db, os.DirFS(catalogMigrations), goose.WithTableName("catalog_goose_db_version"))
		if err == nil {
			_, err = p.Up(ctx)
		}
	}
	if err == nil {
		// The owners' tables leave public (the contract views follow them): a reporting
		// query that reads a table of ingest or catalog-admin without its view fails.
		err = ownTables(ctx, db)
	}
	db.Close()
	if err != nil {
		t.Fatalf("ingest ve catalog-admin şeması kurulamadı: %v", err)
	}
	store, err := Open(ctx, connStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("test veritabanı silinemedi: %v", err)
		}
		admin.Close()
	})
	return store
}

// ownTables moves every table in public to the owners schema.
func ownTables(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, n)
	}
	rows.Close()
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA owners`); err != nil {
		return err
	}
	for _, n := range tables {
		if _, err := db.ExecContext(ctx, `ALTER TABLE public.`+n+` SET SCHEMA owners`); err != nil {
			return err
		}
	}
	return nil
}

func exec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

var ist = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

func at(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, ist) }

type line struct {
	productID, name   string
	qty, price, total int64
}

func sale(t *testing.T, s *Store, store, id, register, cashier string, paid time.Time, total, tax, change int64, lines []line, payments map[string]int64, rate int64) {
	t.Helper()
	exec(t, s, `INSERT INTO sales (store_id, id, register_id, cashier_id, receipt_no, fiscal_no, total, tax_total, change_amount, paid_at)
		VALUES ($1, $2, $3, $4, 1, 'SIM-1', $5, $6, $7, $8)`, store, id, register, cashier, total, tax, change, paid)
	for i, l := range lines {
		exec(t, s, `INSERT INTO sale_lines (store_id, sale_id, line_no, product_id, barcode, name, qty, unit_price, tax_rate, total)
			VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8, $9)`, store, id, i+1, l.productID, l.name, l.qty, l.price, rate, l.total)
	}
	n := 0
	for m, amount := range payments {
		n++
		exec(t, s, `INSERT INTO sale_payments (store_id, sale_id, payment_no, method, amount) VALUES ($1, $2, $3, $4, $5)`, store, id, n, m, amount)
	}
	exec(t, s, `INSERT INTO sale_taxes (store_id, sale_id, rate, gross, tax) VALUES ($1, $2, $3, $4, $5)`, store, id, rate, total, tax)
}

// fixture: two stores; the period is 1–3 October (Istanbul time).
func fixture(t *testing.T, s *Store, checked time.Time) {
	exec(t, s, `INSERT INTO store_links (store_id, name, link, last_seen, pending_events, error, checked_at) VALUES
		('store-1', 'Kadıköy', 'connected', $1, 2, '', $1), ('store-2', 'Moda', 'never', NULL, 0, '', $1)`, checked)
	exec(t, s, `INSERT INTO store_stats (store_id, events, sales, first_event_at, last_event_at) VALUES ('store-1', 5, 2, $1, $2)`, at(1, 10, 0), at(3, 0, 30))

	tomato := line{"p2", "Domates", 1250, 8990, 11238}
	bread := line{"p1", "Ekmek", 2000, 1200, 2400}
	// 1 October: cash 140 TL, change 3.62 TL.
	sale(t, s, "store-1", "s1", "register-1", "ayse", at(1, 10, 0), 13638, 135, 362, []line{tomato, bread}, map[string]int64{"cash": 14000}, 100)
	// 3 October 00:30 Istanbul = 2 October 21:30 UTC: by store time the day is 3 October.
	sale(t, s, "store-1", "s2", "register-2", "mert", at(3, 0, 30), 5000, 833, 0, []line{{"p4", "Deterjan", 1000, 5000, 5000}}, map[string]int64{"card": 5000}, 2000)
	// A sale and a register with the same id can exist in another store too.
	sale(t, s, "store-2", "s1", "register-1", "ayse", at(1, 12, 0), 2400, 24, 0, []line{bread}, map[string]int64{"cash": 2400}, 100)
	// Outside the period: 30 September 23:59 Istanbul.
	sale(t, s, "store-2", "s0", "register-1", "ayse", at(0, 23, 59), 9999, 99, 0, []line{{"p9", "Eski", 1000, 9999, 9999}}, map[string]int64{"cash": 9999}, 100)
	exec(t, s, `INSERT INTO voids (store_id, sale_id, register_id, cashier_id, total, refunded, voided_at) VALUES ('store-1', 's9', 'register-2', 'mert', 999, false, $1)`, at(2, 15, 0))
	// The cashier Kadıköy manages on the store server; "ayse" at Moda is a different person.
	exec(t, s, `INSERT INTO cashiers (store_id, cashier_id, name, role, updated_by, updated_at) VALUES ('store-1', 'ayse', 'Ayşe Yılmaz', 'cashier', 'mağaza', $1)`, at(1, 9, 0))
}

func TestSummary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fixture(t, s, time.Now())
	start, end, err := ledger.Period("2026-10-01", "2026-10-03", ist)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := s.Report(ctx, report.Filter{Start: start, End: end, Location: ist})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Receipts != 3 || sum.Total != 21038 || sum.TaxTotal != 992 || sum.Voided != 1 || sum.VoidedTotal != 999 {
		t.Errorf("toplam %+v", sum)
	}
	// Change deducted from cash: 140 + 24 - 3.62 = 160.38 TL; the total of payments equals the sale.
	if !slices.Equal(sum.Payments, []ledger.PaymentTotal{{Method: "cash", Amount: 16038}, {Method: "card", Amount: 5000}}) {
		t.Errorf("ödemeler %+v", sum.Payments)
	}
	if !slices.Equal(sum.Taxes, []ledger.Tax{{Rate: 100, Gross: 16038, Tax: 159}, {Rate: 2000, Gross: 5000, Tax: 833}}) {
		t.Errorf("KDV %+v", sum.Taxes)
	}
	if !slices.Equal(sum.Stores, []ledger.GroupTotal{
		{StoreID: "store-1", ID: "store-1", Receipts: 2, Total: 18638, Voided: 1, VoidedTotal: 999},
		{StoreID: "store-2", ID: "store-2", Receipts: 1, Total: 2400},
	}) || sum.StoreNames["store-1"] != "Kadıköy" || sum.StoreNames["store-2"] != "Moda" || sum.StoreNames["yok"] != "" {
		t.Errorf("mağazalar %+v %v", sum.Stores, sum.StoreNames)
	}
	// Days with no sales come with zero too.
	if !slices.Equal(sum.Days, []ledger.DayTotal{{Date: "2026-10-01", Receipts: 2, Total: 16038}, {Date: "2026-10-02"}, {Date: "2026-10-03", Receipts: 1, Total: 5000}}) {
		t.Errorf("günler %+v", sum.Days)
	}
	if !slices.Equal(sum.Products, []ledger.ProductTotal{
		{ProductID: "p2", Name: "Domates", Qty: 1250, Total: 11238},
		{ProductID: "p4", Name: "Deterjan", Qty: 1000, Total: 5000},
		{ProductID: "p1", Name: "Ekmek", Qty: 4000, Total: 4800},
	}) {
		t.Errorf("ürünler %+v", sum.Products)
	}
	if !slices.Equal(sum.Cashiers, []ledger.GroupTotal{
		{StoreID: "store-1", ID: "ayse", Receipts: 1, Total: 13638},
		{StoreID: "store-1", ID: "mert", Receipts: 1, Total: 5000, Voided: 1, VoidedTotal: 999},
		{StoreID: "store-2", ID: "ayse", Receipts: 1, Total: 2400},
	}) || sum.CashierNames[[2]string{"store-1", "ayse"}] != "Ayşe Yılmaz" || sum.CashierNames[[2]string{"store-1", "mert"}] != "" {
		t.Errorf("kasiyerler %+v %v", sum.Cashiers, sum.CashierNames)
	}

	// The user's stores: if all are selected it is the same as all the stores;
	// an empty list is none.
	if all, err := s.Report(ctx, report.Filter{Start: start, End: end, Stores: []string{"store-1", "store-2"}, Location: ist}); err != nil ||
		all.Receipts != sum.Receipts || all.Total != sum.Total || len(all.Products) != len(sum.Products) {
		t.Errorf("iki mağaza: %+v %v", all, err)
	}
	if none, err := s.Report(ctx, report.Filter{Start: start, End: end, Stores: []string{}, Location: ist}); err != nil || none.Receipts != 0 || none.Voided != 0 || none.Stores != nil || none.Products != nil {
		t.Errorf("mağazasız: %+v %v", none, err)
	}
	// A single store.
	sum, err = s.Report(ctx, report.Filter{Start: start, End: end, Stores: []string{"store-2"}, Location: ist})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Receipts != 1 || sum.Total != 2400 || sum.Voided != 0 || len(sum.Stores) != 1 ||
		!slices.Equal(sum.Payments, []ledger.PaymentTotal{{Method: "cash", Amount: 2400}, {Method: "card", Amount: 0}}) {
		t.Errorf("store-2: %+v", sum)
	}
	// A period with no sales.
	start, end, _ = ledger.Period("2026-11-01", "2026-11-30", ist)
	if sum, err := s.Report(ctx, report.Filter{Start: start, End: end, Location: ist}); err != nil || sum.Receipts != 0 || sum.Payments != nil || sum.Stores != nil {
		t.Errorf("boş dönem: %+v %v", sum, err)
	}
}

func TestStores(t *testing.T) {
	s := testStore(t)
	checked := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	fixture(t, s, checked)
	start, end, _ := ledger.Period("2026-10-01", "2026-10-01", ist)
	stores, err := s.Stores(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(stores) != 2 {
		t.Fatalf("mağazalar %+v", stores)
	}
	m1, m2 := stores[0], stores[1]
	if m1.ID != "store-1" || m1.Name != "Kadıköy" || m1.Link != domain.LinkConnected || !m1.LastSeen.Equal(checked) || m1.Pending != 2 ||
		!m1.CheckedAt.Equal(checked) || !m1.LastEventAt.Equal(at(3, 0, 30)) || m1.TodayReceipts != 1 || m1.TodayTotal != 13638 {
		t.Errorf("store-1 %+v", m1)
	}
	if m2.Name != "Moda" || m2.Link != domain.LinkNever || !m2.LastSeen.IsZero() || !m2.LastEventAt.IsZero() || m2.TodayReceipts != 1 || m2.TodayTotal != 2400 {
		t.Errorf("store-2 %+v", m2)
	}
}

// The sequence number gaps in the events the registers sent (ingest writes them)
// show in the store's state; a store with no gaps has none.
func TestStoresMissingEvents(t *testing.T) {
	s := testStore(t)
	fixture(t, s, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	exec(t, s, `INSERT INTO event_ranges (store_id, register_id, producer, from_seq, to_seq) VALUES
		('store-1', 'register-1', 'sales', 1, 4), ('store-1', 'register-1', 'sales', 8, 9), ('store-1', 'register-1', 'staff', 1, 3),
		('store-2', 'register-1', 'sales', 1, 5)`)
	start, end, _ := ledger.Period("2026-10-01", "2026-10-01", ist)
	stores, err := s.Stores(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(stores[0].MissingEvents) != 1 || stores[0].MissingEvents[0].Register != "register-1" || stores[0].MissingEvents[0].Producer != "sales" ||
		stores[0].MissingEvents[0].From != 5 || stores[0].MissingEvents[0].To != 7 {
		t.Errorf("store-1 eksik olaylar: %+v", stores[0].MissingEvents)
	}
	if len(stores[1].MissingEvents) != 0 {
		t.Errorf("store-2 eksik olay göstermemeli: %+v", stores[1].MissingEvents)
	}
}

// The SQL of users and sessions is tested on two databases in
// pkg/personnel/personneldb; here we check that the schema set up with headquarters'
// migrations fits it.
func TestPeople(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	people := s.People()

	ali := personnel.User{ID: "ali", Name: "Ali Kaya", Role: personnel.RoleReport, Stores: []string{"store-1", "store-2"},
		PasswordHash: "özet", CreatedAt: now, UpdatedBy: "kurulum", UpdatedAt: now}
	req := personnel.SaveRequest{User: ali, New: true, Change: personnel.Change{ID: "ali", At: now, By: "kurulum", Detail: "eklendi"}}
	if err := people.Save(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got, err := people.User(ctx, "ali"); err != nil || !reflect.DeepEqual(got, ali) {
		t.Errorf("kullanıcı %+v %v", got, err)
	}
	key := login.TokenHash("oturum")
	if err := people.Put(ctx, key, login.Session{Subject: "ali", Expires: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := people.Get(ctx, key, now); err != nil || !ok {
		t.Errorf("oturum: %v %v", ok, err)
	}
	req.New, req.CloseSessions = false, true
	if err := people.Save(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := people.Get(ctx, key, now); ok {
		t.Error("kullanıcı kaydıyla birlikte oturumu silinmedi")
	}

	// A user added before the role (migration) becomes an administrator.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (id, name, password_hash, created_at, updated_at) VALUES ('eski', 'Eski', 'x', $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	if u, err := people.User(ctx, "eski"); err != nil || u.Role != personnel.RoleAdmin || !u.AllStores {
		t.Errorf("eski kullanıcı %+v %v", u, err)
	}
}

// The prices the stores reported are read together with the headquarters prices.
func TestStorePrices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	exec(t, s, `INSERT INTO store_links (store_id, name, link, pending_events, checked_at) VALUES ('store-1', 'Kadıköy', 'connected', 0, now())`)
	exec(t, s, `INSERT INTO catalog_products (code, name, search_name, unit, tax_rate, quick_key, updated_by, updated_at, version)
		VALUES ('ekmek', 'Ekmek 250 g', 'ekmek 250 g', 'piece', 100, false, 'ayse', now(), 1)`)
	exec(t, s, `INSERT INTO catalog_prices (code, from_ms, amount) VALUES ('ekmek', 0, 1500), ('ekmek', $1, 1650)`, monday.UnixMilli())
	exec(t, s, `INSERT INTO store_prices (store_id, product_id, product_name, prices, updated_by, updated_at) VALUES
		('store-1', 'ekmek', 'eski ad', $1, 'mağaza', $2),
		('store-2', 'yok', 'Katalogda Olmayan', '[]', 'mağaza', $2)`,
		`[{"amount": 1800}, {"amount": 0, "from": "2026-11-01T21:00:00Z", "clear": true}]`, at(29, 9, 0))
	rows, err := s.StorePrices(ctx, "ekmek")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("satırlar %+v", rows)
	}
	r := rows[0]
	if r.StoreName != "Kadıköy" || r.ProductName != "Ekmek 250 g" || r.UpdatedBy != "mağaza" || len(r.StorePrices) != 2 ||
		r.StorePrices[0] != (domain.PricePoint{Amount: 1800}) || !r.StorePrices[1].Clear || !r.StorePrices[1].From.Equal(monday) ||
		len(r.HeadquartersPrices) != 2 || r.HeadquartersPrices[1] != (domain.PricePoint{Amount: 1650, From: monday}) {
		t.Errorf("satır %+v", r)
	}
	all, err := s.StorePrices(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("bütün satırlar %+v %v", all, err)
	}
	for _, r := range all {
		if r.ProductID == "yok" && (r.StoreName != "store-2" || r.ProductName != "Katalogda Olmayan" || r.HeadquartersPrices != nil) {
			t.Errorf("katalogda olmayan ürün %+v", r)
		}
	}
}
