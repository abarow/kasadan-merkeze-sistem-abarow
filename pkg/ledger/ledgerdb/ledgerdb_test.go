// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledgerdb

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
)

// databases are the databases the test runs on: always SQLite;
// PostgreSQL too if ABAROW_TEST_POSTGRES is given (e.g. postgres://postgres:trial@localhost:5432/postgres).
func databases(t *testing.T) map[string]struct {
	db *sql.DB
	d  Dialect
} {
	t.Helper()
	ctx := context.Background()
	out := map[string]struct {
		db *sql.DB
		d  Dialect
	}{}
	schema := func(file string) string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	lite, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	lite.SetMaxOpenConns(1)
	t.Cleanup(func() { lite.Close() })
	if _, err := lite.ExecContext(ctx, schema("testdata/sqlite.sql")); err != nil {
		t.Fatal(err)
	}
	out["sqlite"] = struct {
		db *sql.DB
		d  Dialect
	}{lite, SQLite}

	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		return out
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("abarow_ledger_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	pg := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		pg.Close()
		admin.ExecContext(ctx, "DROP DATABASE "+name)
	})
	if _, err := pg.ExecContext(ctx, schema("testdata/postgres.sql")); err != nil {
		t.Fatal(err)
	}
	out["postgres"] = struct {
		db *sql.DB
		d  Dialect
	}{pg, Postgres}
	return out
}

// sample is random but repeatable sales and cancellations: two
// stores, registers, cashiers (one without a cashier), times close to midnight,
// change, meal card, lines with no id (entered by hand).
func sample() ([]ledger.Sale, []ledger.Void) {
	r := rand.New(rand.NewPCG(1, 2))
	ist, _ := time.LoadLocation("Europe/Istanbul")
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, ist)
	stores := []string{"store-1", "store-2"}
	cashiers := []string{"ayse", "mert", ""}
	products := []ledger.Line{
		{ProductID: "p1", Name: "Ekmek", UnitPrice: 1500, TaxRate: 100},
		{ProductID: "p2", Name: "Domates", UnitPrice: 8990, TaxRate: 100},
		{ProductID: "p3", Name: "Şampuan", UnitPrice: 12950, TaxRate: 2000},
		{ProductID: "", Name: "Elle girilen", UnitPrice: 1000, TaxRate: 2000},
	}
	var sales []ledger.Sale
	var voids []ledger.Void
	receipt := map[string]int64{}
	for i := range 300 {
		store := stores[r.IntN(2)]
		register := fmt.Sprintf("register-%d", 1+r.IntN(2))
		s := ledger.Sale{
			ID: fmt.Sprintf("s%d", i), StoreID: store, RegisterID: register, CashierID: cashiers[r.IntN(3)],
			PaidAt: base.Add(time.Duration(r.IntN(3*24*60)) * time.Minute).Add(time.Duration(r.IntN(60)) * time.Second),
		}
		receipt[store+register]++
		s.ReceiptNo = receipt[store+register]
		s.FiscalNo = fmt.Sprintf("SIM-%06d", s.ReceiptNo)
		taxes := map[money.Rate]*ledger.Tax{}
		for range 1 + r.IntN(4) {
			l := products[r.IntN(len(products))]
			l.Qty = money.Qty(250 * (1 + r.IntN(8)))
			s.Lines = append(s.Lines, l)
			s.Total += l.Total()
			if taxes[l.TaxRate] == nil {
				taxes[l.TaxRate] = &ledger.Tax{Rate: l.TaxRate}
			}
			taxes[l.TaxRate].Gross += l.Total()
		}
		for _, rate := range []money.Rate{100, 2000} {
			if t := taxes[rate]; t != nil {
				t.Tax = t.Gross * money.Money(rate) / money.Money(10000+rate)
				s.TaxTotal += t.Tax
				s.Taxes = append(s.Taxes, *t)
			}
		}
		switch r.IntN(4) {
		case 0:
			s.Payments = []ledger.Payment{{Method: ledger.Card, Amount: s.Total}}
		case 1:
			s.Payments = []ledger.Payment{{Method: ledger.MealCard, Amount: s.Total}}
		case 2:
			half := s.Total / 2
			s.Payments = []ledger.Payment{{Method: ledger.Card, Amount: half}, {Method: ledger.Cash, Amount: s.Total - half}}
		default:
			paid := (s.Total/10000 + 1) * 10000
			s.Payments = []ledger.Payment{{Method: ledger.Cash, Amount: paid}}
			s.Change = paid - s.Total
		}
		sales = append(sales, s)
		if r.IntN(10) == 0 {
			voids = append(voids, ledger.Void{SaleID: fmt.Sprintf("v%d", i), StoreID: store, RegisterID: register,
				CashierID: cashiers[r.IntN(3)], Total: money.Money(100 * (1 + r.IntN(100))), Refunded: r.IntN(2) == 0,
				At: s.PaidAt.Add(time.Minute)})
		}
	}
	return sales, voids
}

// TestReportMatchesBuild checks that the report in the database (grouped in SQL) is
// exactly the same as the in-memory calculation (ledger.Build).
func TestReportMatchesBuild(t *testing.T) {
	ctx := context.Background()
	ist, _ := time.LoadLocation("Europe/Istanbul")
	sales, voids := sample()
	day := func(d int) time.Time { return time.Date(2026, 10, 28+d, 0, 0, 0, 0, ist) }
	filters := map[string]ledger.Filter{
		"bütün mağazalar, üç gün":   {Start: day(0), End: day(3), Location: ist},
		"bir mağaza, bir gün":       {Start: day(1), End: day(2), Stores: []string{"store-2"}, Location: ist},
		"iki mağaza, ilk 3 ürün":    {Start: day(0), End: day(2), Stores: []string{"store-1", "store-2"}, Location: ist, MaxProducts: 3},
		"hiçbir mağaza":             {Start: day(0), End: day(3), Stores: []string{}, Location: ist},
		"UTC günleri":               {Start: day(1).UTC(), End: day(2).UTC(), Location: time.UTC},
		"satış olmayan dönem dahil": {Start: day(-2), End: day(1), Location: ist},
	}
	for name, db := range databases(t) {
		t.Run(name, func(t *testing.T) {
			tx, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range sales {
				if ok, err := db.d.SaveSale(ctx, tx, s); err != nil || !ok {
					t.Fatalf("sale %s: %v %v", s.ID, ok, err)
				}
			}
			// A repeated sale is not written.
			if ok, err := db.d.SaveSale(ctx, tx, sales[0]); err != nil || ok {
				t.Fatalf("repeated sale: %v %v", ok, err)
			}
			for _, v := range voids {
				for range 2 {
					if err := db.d.SaveVoid(ctx, tx, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			for fname, f := range filters {
				got, err := db.d.Report(ctx, db.db, f)
				if err != nil {
					t.Fatalf("%s: %v", fname, err)
				}
				ss, vs := pick(f, sales, voids)
				want := ledger.Build(f, ss, vs)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s:\ndatabase %+v\nmemory   %+v", fname, got, want)
				}
				if fname == "bütün mağazalar, üç gün" && (got.Receipts != int64(len(sales)) || got.Voided != int64(len(voids))) {
					t.Errorf("%s: %d receipts, %d cancellations", fname, got.Receipts, got.Voided)
				}
			}
		})
	}
}

// pick selects the sales and cancellations that fall in the filter.
func pick(f ledger.Filter, sales []ledger.Sale, voids []ledger.Void) ([]ledger.Sale, []ledger.Void) {
	in := func(store string, at time.Time) bool {
		if at.Before(f.Start) || !at.Before(f.End) {
			return false
		}
		if f.Stores == nil {
			return true
		}
		for _, s := range f.Stores {
			if s == store {
				return true
			}
		}
		return false
	}
	var ss []ledger.Sale
	for _, s := range sales {
		if in(s.StoreID, s.PaidAt) {
			ss = append(ss, s)
		}
	}
	var vs []ledger.Void
	for _, v := range voids {
		if in(v.StoreID, v.At) {
			vs = append(vs, v)
		}
	}
	return ss, vs
}

func TestUnfinishedAndShifts(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	for name, db := range databases(t) {
		t.Run(name, func(t *testing.T) {
			// The attempts come in mixed order: the first time and the last reason stay.
			u := ledger.Unfinished{SaleID: "s1", StoreID: "m", RegisterID: "k", CashierID: "ayse", Total: 500}
			for _, a := range []struct {
				at     time.Time
				reason string
			}{{t0.Add(time.Minute), "ikinci"}, {t0, "ilk"}, {t0.Add(2 * time.Minute), "üçüncü"}, {t0.Add(time.Minute), "ikinci"}} {
				u.At, u.Reason = a.at, a.reason
				if err := db.d.SaveUnfinished(ctx, db.db, u); err != nil {
					t.Fatal(err)
				}
			}
			var since, last time.Time
			var reason string
			if err := db.db.QueryRowContext(ctx, `SELECT since, last_attempt, reason FROM unfinished`).
				Scan(ScanTime(&since), ScanTime(&last), &reason); err != nil {
				t.Fatal(err)
			}
			if !since.Equal(t0) || !last.Equal(t0.Add(2*time.Minute)) || reason != "üçüncü" {
				t.Errorf("not receipted: %v %v %q", since, last, reason)
			}

			// The result is the same even if the closing comes before the opening.
			open := ledger.Shift{StoreID: "m", RegisterID: "k", ID: 3, CashierID: "ayse", CashierName: "Ayşe", OpenedAt: t0, OpeningCash: 50000}
			closed := open
			closed.ClosedAt, closed.ClosedBy, closed.Receipts, closed.Total = t0.Add(8*time.Hour), "ali", 12, 34500
			closed.ExpectedCash, closed.CountedCash, closed.Difference = 60000, 59900, -100
			for _, sh := range []ledger.Shift{closed, open, open} {
				if err := db.d.SaveShift(ctx, db.db, sh); err != nil {
					t.Fatal(err)
				}
			}
			var got ledger.Shift
			if err := db.db.QueryRowContext(ctx, `
				SELECT store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash, closed_at, closed_by,
				       receipts, total, expected_cash, counted_cash, difference FROM shifts`).
				Scan(&got.StoreID, &got.RegisterID, &got.ID, &got.CashierID, &got.CashierName, ScanTime(&got.OpenedAt), &got.OpeningCash,
					ScanTime(&got.ClosedAt), &got.ClosedBy, &got.Receipts, &got.Total, &got.ExpectedCash, &got.CountedCash, &got.Difference); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, closed) {
				t.Errorf("vardiya:\n%+v\nistenen\n%+v", got, closed)
			}
		})
	}
}

func TestQ(t *testing.T) {
	if got := Postgres.Q("a = ? AND b IN (?, ?)"); got != "a = $1 AND b IN ($2, $3)" {
		t.Error(got)
	}
	if got := SQLite.Q("a = ?"); got != "a = ?" {
		t.Error(got)
	}
}
