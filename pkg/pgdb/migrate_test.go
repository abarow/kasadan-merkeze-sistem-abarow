// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgdb_test

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"
	"time"

	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/pgdb/pgdbtest"
)

func gooseFiles(files ...string) fstest.MapFS {
	m := fstest.MapFS{}
	for i := 0; i+1 < len(files); i += 2 {
		m[files[i]] = &fstest.MapFile{Data: []byte("-- +goose Up\n" + files[i+1] + "\n")}
	}
	return m
}

// TestMigrationGivesWay: a schema change waits behind a long reader of the table (as the nightly
// pg_dump). While it waits the other queries of the table go on (they wait at most
// MigrationLockTimeout); when the reader is done the change is made.
func TestMigrationGivesWay(t *testing.T) {
	dsn := pgdbtest.URL(t)
	ctx := context.Background()
	first := gooseFiles("00001_t.sql", "CREATE TABLE t (id INT PRIMARY KEY); INSERT INTO t VALUES (1);")
	if err := pgdb.Migrate(ctx, dsn, first); err != nil {
		t.Fatal(err)
	}
	lock, retry := pgdb.MigrationLockTimeout, pgdb.MigrationRetry
	pgdb.MigrationLockTimeout, pgdb.MigrationRetry = 200*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { pgdb.MigrationLockTimeout, pgdb.MigrationRetry = lock, retry })

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reader, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- pgdb.Migrate(ctx, dsn, gooseFiles("00001_t.sql", "CREATE TABLE t (id INT PRIMARY KEY); INSERT INTO t VALUES (1);",
			"00002_x.sql", "ALTER TABLE t ADD COLUMN x INT;"))
	}()
	// The other queries of the table: none waits longer than a second.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		qctx, cancel := context.WithTimeout(ctx, time.Second)
		err := db.QueryRowContext(qctx, "SELECT count(*) FROM t").Scan(&n)
		cancel()
		if err != nil {
			t.Fatalf("a query waited behind the migration: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("the migration ended while the reader held the table: %v", err)
	default:
	}
	if err := reader.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the migration did not finish after the reader was done")
	}
	if err := db.QueryRow("SELECT count(x) FROM t").Scan(&n); err != nil {
		t.Fatalf("the column was not added: %v", err)
	}
}

// TestMigrationErrors: an error that is not a lock wait is returned at once.
func TestMigrationErrors(t *testing.T) {
	dsn := pgdbtest.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pgdb.Migrate(ctx, dsn, gooseFiles("00001_bad.sql", "CREATE TABLE;")); err == nil {
		t.Fatal("a broken migration was accepted")
	}
}
