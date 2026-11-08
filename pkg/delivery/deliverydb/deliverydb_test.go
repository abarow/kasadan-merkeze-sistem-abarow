// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package deliverydb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/sqldialect"
)

type testDB struct {
	db *sql.DB
	d  sqldialect.Dialect
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
	sqlite, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlite.Close() })
	if _, err := sqlite.ExecContext(ctx, schema("testdata/sqlite.sql")); err != nil {
		t.Fatal(err)
	}
	out["sqlite"] = testDB{sqlite, sqldialect.SQLite}

	if dsn := os.Getenv("ABAROW_TEST_POSTGRES"); dsn != "" {
		pg, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("deliverydb_%d", time.Now().UnixNano())
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

func TestIssueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			s := Store{Dialect: d.d}
			n := 0
			newID := func() string { n++; return fmt.Sprintf("receipt-%d", n) }
			first, err := s.Issue(ctx, d.db, "store-1", "ABAROW_STORE", "e1", newID, now)
			if err != nil || !first.New || first.ReceiptID != "receipt-1" || first.EventID != "e1" {
				t.Fatalf("first receipt = %+v, %v", first, err)
			}
			// The same event comes again: the same receipt id, not new.
			again, err := s.Issue(ctx, d.db, "store-1", "ABAROW_STORE", "e1", newID, now.Add(time.Hour))
			if err != nil || again.New || again.ReceiptID != "receipt-1" || !again.At.Equal(now) {
				t.Fatalf("repeated receipt = %+v, %v; should be the same as the first", again, err)
			}
			other, err := s.Issue(ctx, d.db, "store-1", "ABAROW_STORE", "e2", newID, now)
			if err != nil || !other.New || other.ReceiptID == first.ReceiptID {
				t.Fatalf("second event = %+v, %v", other, err)
			}
		})
	}
}

func TestIssueRollsBackWithEvent(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			s := Store{Dialect: d.d}
			tx, err := d.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Issue(ctx, tx, "m", "S", "e1", func() string { return "r1" }, now); err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// If the event could not be written and the transaction was rolled back there is no receipt either.
			got, err := s.Issue(ctx, d.db, "m", "S", "e1", func() string { return "r2" }, now)
			if err != nil || !got.New || got.ReceiptID != "r2" {
				t.Fatalf("a withdrawn receipt remained: %+v, %v", got, err)
			}
		})
	}
}

func TestReceivedRoundTrip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			s := Store{Dialect: d.d}
			r := delivery.Receipt{EventID: "e1", ReceiptID: "r1", At: now}
			if ok, err := s.SaveReceived(ctx, d.db, r, now); err != nil || !ok {
				t.Fatalf("SaveReceived = %v, %v", ok, err)
			}
			// If the same event's receipt comes again the first is valid.
			if ok, err := s.SaveReceived(ctx, d.db, delivery.Receipt{EventID: "e1", ReceiptID: "r1b", At: now}, now); err != nil || ok {
				t.Fatalf("SaveReceived again = %v, %v; false expected", ok, err)
			}
			got, err := s.Received(ctx, d.db, []string{"e1", "e2", "e3"})
			if err != nil || !got["e1"] || got["e2"] || got["e3"] || len(got) != 1 {
				t.Fatalf("Received = %v, %v", got, err)
			}
			if got, err := s.Received(ctx, d.db, nil); err != nil || len(got) != 0 {
				t.Fatalf("empty query = %v, %v", got, err)
			}
		})
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for name, d := range databases(t) {
		t.Run(name, func(t *testing.T) {
			s := Store{Dialect: d.d}
			for i := range 5 {
				at := now.Add(time.Duration(i-5) * 24 * time.Hour) // 5, 4, 3, 2 and 1 days ago
				id := fmt.Sprintf("e%d", i)
				if _, err := s.Issue(ctx, d.db, "m", "S", id, func() string { return "r" + id }, at); err != nil {
					t.Fatal(err)
				}
				if _, err := s.SaveReceived(ctx, d.db, delivery.Receipt{EventID: id, ReceiptID: "r" + id, At: at}, at); err != nil {
					t.Fatal(err)
				}
			}
			before := now.Add(-2*24*time.Hour - time.Minute) // older than 2 days: e0, e1, e2
			if n, err := s.PruneIssued(ctx, d.db, before, 2); err != nil || n != 2 {
				t.Fatalf("PruneIssued (limit 2) = %d, %v", n, err)
			}
			if n, err := s.PruneIssued(ctx, d.db, before, 10); err != nil || n != 1 {
				t.Fatalf("PruneIssued = %d, %v; the third old receipt was expected", n, err)
			}
			if n, err := s.PruneReceived(ctx, d.db, before, 10); err != nil || n != 3 {
				t.Fatalf("PruneReceived = %d, %v", n, err)
			}
			got, err := s.Received(ctx, d.db, []string{"e0", "e1", "e2", "e3", "e4"})
			if err != nil || len(got) != 2 || !got["e3"] || !got["e4"] {
				t.Fatalf("receipts left = %v, %v; e3 and e4 expected", got, err)
			}
			// A newer event that comes again still gets its receipt.
			again, err := s.Issue(ctx, d.db, "m", "S", "e4", func() string { return "new" }, now)
			if err != nil || again.New || again.ReceiptID != "re4" {
				t.Fatalf("Issue after prune = %+v, %v", again, err)
			}
		})
	}
}
