// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"abarow.com/pkg/sqldialect"
)

// tables are the table layouts the test tries: register (with store and register
// columns), store server (with a store column), headquarters (no column, fixed store).
var tables = []struct {
	t      Table
	sqlite string
	pg     string
}{
	{Table{Name: "outbox", Store: true, Register: true},
		`CREATE TABLE outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			store_id TEXT NOT NULL, register_id TEXT NOT NULL, payload BLOB NOT NULL, content_type TEXT NOT NULL,
			occurred_at TEXT NOT NULL, published_at TEXT)`,
		`CREATE TABLE outbox (seq BIGSERIAL PRIMARY KEY, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			store_id TEXT NOT NULL, register_id TEXT NOT NULL, payload BYTEA NOT NULL, content_type TEXT NOT NULL,
			occurred_at TIMESTAMPTZ NOT NULL, published_at TIMESTAMPTZ)`},
	{Table{Name: "store_outbox", Store: true},
		`CREATE TABLE store_outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			store_id TEXT NOT NULL, payload BLOB NOT NULL, content_type TEXT NOT NULL, occurred_at TEXT NOT NULL, published_at TEXT)`,
		`CREATE TABLE store_outbox (seq BIGSERIAL PRIMARY KEY, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			store_id TEXT NOT NULL, payload BYTEA NOT NULL, content_type TEXT NOT NULL, occurred_at TIMESTAMPTZ NOT NULL, published_at TIMESTAMPTZ)`},
	{Table{Name: "headquarters_outbox", StoreID: "headquarters"},
		`CREATE TABLE headquarters_outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			payload BLOB NOT NULL, content_type TEXT NOT NULL, occurred_at TEXT NOT NULL, published_at TEXT)`,
		`CREATE TABLE headquarters_outbox (seq BIGSERIAL PRIMARY KEY, id TEXT NOT NULL UNIQUE, type TEXT NOT NULL, subject TEXT NOT NULL,
			payload BYTEA NOT NULL, content_type TEXT NOT NULL, occurred_at TIMESTAMPTZ NOT NULL, published_at TIMESTAMPTZ)`},
}

func TestTable(t *testing.T) {
	ctx := context.Background()
	dbs := map[string]*sql.DB{}
	lite, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	lite.SetMaxOpenConns(1)
	defer lite.Close()
	dbs["sqlite"] = lite
	if dsn := os.Getenv("ABAROW_TEST_POSTGRES"); dsn != "" {
		admin, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		name := fmt.Sprintf("abarow_outbox_%d_%d", os.Getpid(), time.Now().UnixNano())
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		defer admin.ExecContext(ctx, "DROP DATABASE "+name)
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Database = name
		pg := stdlib.OpenDB(*cfg)
		defer pg.Close()
		dbs["postgres"] = pg
	}
	at := time.Date(2026, 10, 30, 9, 0, 0, 123456000, time.UTC) // PostgreSQL mikrosaniye saklar
	for name, db := range dbs {
		d := sqldialect.SQLite
		if name == "postgres" {
			d = sqldialect.Postgres
		}
		for _, tt := range tables {
			tb := tt.t
			tb.Dialect = d
			t.Run(name+"/"+tb.Name, func(t *testing.T) {
				ddl := tt.sqlite
				if name == "postgres" {
					ddl = tt.pg
				}
				if _, err := db.ExecContext(ctx, ddl); err != nil {
					t.Fatal(err)
				}
				msgs := []Message{
					{ID: "e1", Type: "com.abarow.a.v1", Subject: "a.v1", StoreID: "store-1", RegisterID: "register-1",
						Payload: []byte{1, 2}, ContentType: "application/protobuf", OccurredAt: at},
					{ID: "e2", Type: "com.abarow.b.v1", Subject: "b.v1", StoreID: "store-1", RegisterID: "register-1",
						Payload: []byte{3}, ContentType: "application/protobuf", OccurredAt: at.Add(time.Second)},
				}
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := tb.Enqueue(ctx, tx, msgs...); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				got, err := tb.Pending(ctx, db, 10)
				if err != nil || len(got) != 2 {
					t.Fatalf("bekleyen: %+v %v", got, err)
				}
				for i, p := range got {
					want := msgs[i]
					if !tb.Store {
						want.StoreID = "headquarters"
					}
					if !tb.Register {
						want.RegisterID = ""
					}
					if !reflect.DeepEqual(p.Message, want) {
						t.Errorf("%d:\n%+v\nistenen\n%+v", i, p.Message, want)
					}
				}
				if err := tb.MarkPublished(ctx, db, got[0].Seq, at); err != nil {
					t.Fatal(err)
				}
				n, err := tb.CountPending(ctx, db)
				rest, _ := tb.Pending(ctx, db, 10)
				if err != nil || n != 1 || len(rest) != 1 || rest[0].ID != "e2" {
					t.Fatalf("işaretledikten sonra: %d %v %+v", n, err, rest)
				}

				// Prune removes only what was published before the moment; the pending message stays.
				if n, err := tb.Prune(ctx, db, at.Add(time.Minute), 10); err != nil || n != 1 {
					t.Fatalf("Prune = %d, %v; e1 expected", n, err)
				}
				if left, err := tb.Pending(ctx, db, 10); err != nil || len(left) != 1 || left[0].ID != "e2" {
					t.Fatalf("pending after Prune: %+v %v", left, err)
				}
				// PruneDelivered also waits for the receipt.
				if err := tb.MarkPublished(ctx, db, rest[0].Seq, at.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS delivery_received (event_id TEXT PRIMARY KEY)`); err != nil {
					t.Fatal(err)
				}
				if n, err := tb.PruneDelivered(ctx, db, at.Add(2*time.Hour), 10); err != nil || n != 0 {
					t.Fatalf("PruneDelivered without a receipt = %d, %v", n, err)
				}
				if u, err := tb.Undelivered(ctx, db, at.Add(2*time.Hour)); err != nil || !u {
					t.Fatalf("Undelivered = %v, %v; e2 has no receipt", u, err)
				}
				if u, err := tb.Undelivered(ctx, db, at); err != nil || u {
					t.Fatalf("Undelivered before e2 = %v, %v", u, err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO delivery_received (event_id) VALUES ('e2')`); err != nil {
					t.Fatal(err)
				}
				if n, err := tb.PruneDelivered(ctx, db, at.Add(2*time.Hour), 10); err != nil || n != 1 {
					t.Fatalf("PruneDelivered = %d, %v", n, err)
				}
				if u, err := tb.Undelivered(ctx, db, at.Add(2*time.Hour)); err != nil || u {
					t.Fatalf("Undelivered after the receipt = %v, %v", u, err)
				}
				var rows int
				if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+tb.Name).Scan(&rows); err != nil || rows != 0 {
					t.Fatalf("rows left = %d, %v", rows, err)
				}
				if _, err := db.ExecContext(ctx, `DELETE FROM delivery_received`); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
