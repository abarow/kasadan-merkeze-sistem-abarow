// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sequencedb_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"abarow.com/pkg/sequence"
	"abarow.com/pkg/sequence/sequencedb"
	"abarow.com/pkg/sqldialect"
)

type testDB struct {
	db      *sql.DB
	dialect sqldialect.Dialect
}

// databases are the databases the test runs on: always SQLite;
// PostgreSQL too if ABAROW_TEST_POSTGRES is given.
func databases(t *testing.T) map[string]testDB {
	t.Helper()
	ctx := context.Background()
	out := map[string]testDB{}
	schema := func(file string) string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	lite, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	lite.SetMaxOpenConns(1)
	t.Cleanup(func() { lite.Close() })
	if _, err := lite.ExecContext(ctx, schema("testdata/sqlite.sql")); err != nil {
		t.Fatal(err)
	}
	out["sqlite"] = testDB{lite, sqldialect.SQLite}

	if dsn := os.Getenv("ABAROW_TEST_POSTGRES"); dsn != "" {
		pg, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("sequencedb_%d", time.Now().UnixNano())
		if _, err := pg.ExecContext(ctx, "CREATE SCHEMA "+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pg.ExecContext(context.Background(), "DROP SCHEMA "+name+" CASCADE"); pg.Close() })
		pg.SetMaxOpenConns(1)
		if _, err := pg.ExecContext(ctx, "SET search_path TO "+name); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.ExecContext(ctx, schema("testdata/postgres.sql")); err != nil {
			t.Fatal(err)
		}
		out["postgres"] = testDB{pg, sqldialect.Postgres}
	}
	return out
}

func ranges(t *testing.T, d testDB) int {
	t.Helper()
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM event_ranges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func record(t *testing.T, tr sequencedb.Tracker, store, register, producer string, seqs ...int64) {
	t.Helper()
	for _, s := range seqs {
		if err := tr.Record(context.Background(), store, register, producer, s); err != nil {
			t.Fatalf("Record(%d): %v", s, err)
		}
	}
}

func gaps(t *testing.T, tr sequencedb.Tracker, store string, limit int) []sequence.Gap {
	t.Helper()
	g, err := tr.Gaps(context.Background(), store, limit)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// Events that come in order are gathered in a single range; there is no gap.
func TestInOrderIsOneRange(t *testing.T) {
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			tr := sequencedb.Tracker{DB: d.db, Dialect: d.dialect}
			record(t, tr, "m1", "register-1", "sales", 1, 2, 3, 4, 5)
			if g := gaps(t, tr, "", 0); len(g) != 0 {
				t.Errorf("no gap was expected: %v", g)
			}
			if n := ranges(t, d); n != 1 {
				t.Errorf("%d ranges, want 1", n)
			}
			if last, err := tr.Last(context.Background(), "m1", "register-1", "sales"); err != nil || last != 5 {
				t.Errorf("Last = %d %v", last, err)
			}
		})
	}
}

// If a number is missing in between the gap shows; when the missing event comes later the gap
// closes and the ranges merge.
func TestGapAppearsAndCloses(t *testing.T) {
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			tr := sequencedb.Tracker{DB: d.db, Dialect: d.dialect}
			record(t, tr, "m1", "register-1", "sales", 1, 2, 5)
			g := gaps(t, tr, "m1", 0)
			want := []sequence.Gap{{Store: "m1", Register: "register-1", Producer: "sales", From: 3, To: 4}}
			if fmt.Sprint(g) != fmt.Sprint(want) || sequence.Total(g) != 2 {
				t.Fatalf("gap %v, want %v", g, want)
			}
			record(t, tr, "m1", "register-1", "sales", 3)
			if g := gaps(t, tr, "m1", 0); len(g) != 1 || g[0].From != 4 || g[0].To != 4 {
				t.Errorf("3 geldi: %v", g)
			}
			record(t, tr, "m1", "register-1", "sales", 4)
			if g := gaps(t, tr, "m1", 0); len(g) != 0 {
				t.Errorf("4 came, no gap should remain: %v", g)
			}
			if n := ranges(t, d); n != 1 {
				t.Errorf("after the gap closed %d ranges, want 1", n)
			}
		})
	}
}

// Events come at least once: if the same number comes again nothing changes.
// Numbers that come out of order merge correctly too.
func TestDuplicatesAndOutOfOrder(t *testing.T) {
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			tr := sequencedb.Tracker{DB: d.db, Dialect: d.dialect}
			record(t, tr, "m1", "register-1", "sales", 1, 2, 2, 1, 3, 3)
			if g := gaps(t, tr, "", 0); len(g) != 0 || ranges(t, d) != 1 {
				t.Errorf("repeats broke the range: %v, %d ranges", g, ranges(t, d))
			}
			// 9, 5, 7, 6, 8, 4: first scattered, then all merge.
			record(t, tr, "m1", "register-1", "sales", 9, 5, 7)
			if g := gaps(t, tr, "", 0); len(g) != 3 {
				t.Errorf("scattered numbers: %v", g)
			}
			record(t, tr, "m1", "register-1", "sales", 6, 8, 4)
			if g := gaps(t, tr, "", 0); len(g) != 0 || ranges(t, d) != 1 {
				t.Errorf("when all have come there should be a single range: %v, %d ranges", g, ranges(t, d))
			}
		})
	}
}

// What is before the first number seen does not count as a gap (old events from before
// numbering); store, register and sender are independent of one another.
func TestBaselineAndIndependence(t *testing.T) {
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			tr := sequencedb.Tracker{DB: d.db, Dialect: d.dialect}
			record(t, tr, "m1", "register-1", "sales", 100, 101, 103)
			record(t, tr, "m1", "register-1", "staff", 1, 2, 3)
			record(t, tr, "m1", "register-2", "sales", 1, 3)
			record(t, tr, "m2", "register-1", "sales", 1, 5)

			all := gaps(t, tr, "", 0)
			if len(all) != 3 || sequence.Total(all) != 1+1+3 {
				t.Fatalf("all gaps: %v", all)
			}
			m1 := gaps(t, tr, "m1", 0)
			if len(m1) != 2 {
				t.Fatalf("m1: %v", m1)
			}
			// By register and sender order.
			if m1[0].Register != "register-1" || m1[0].Producer != "sales" || m1[0].From != 102 || m1[1].Register != "register-2" {
				t.Errorf("m1's order: %v", m1)
			}
			if one := gaps(t, tr, "", 1); len(one) != 1 {
				t.Errorf("limit 1: %v", one)
			}
			if last, _ := tr.Last(context.Background(), "m1", "register-9", "sales"); last != 0 {
				t.Errorf("Last of the sender that never came is %d", last)
			}
		})
	}
}
