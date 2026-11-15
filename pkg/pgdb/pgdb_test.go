// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgdb_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/pgdb/pgdbtest"
)

func migrations(table string) fstest.MapFS {
	return fstest.MapFS{"migrations/00001_init.sql": {Data: []byte(
		"-- +goose Up\nCREATE TABLE " + table + " (id BIGINT PRIMARY KEY);\n-- +goose Down\nDROP TABLE " + table + ";\n")}}
}

// Two services use the same database: each has its own tables and its own migration
// history, the same table name does not collide.
func TestSchemasAreSeparate(t *testing.T) {
	ctx := context.Background()
	dsn := pgdbtest.URL(t)
	a, err := pgdb.Open(ctx, dsn, "sales", migrations("items"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := pgdb.Open(ctx, dsn, "staff", migrations("items"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := a.ExecContext(ctx, `INSERT INTO items VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the staff schema sees the rows of the sales schema: %d", n)
	}
	// Opening again does not apply the migration a second time.
	again, err := pgdb.Open(ctx, dsn, "sales", migrations("items"))
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestBadAddressAndSchema(t *testing.T) {
	ctx := context.Background()
	if _, err := pgdb.Open(ctx, "postgres://localhost/x", "Bad-Name", migrations("t")); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Errorf("bad schema name: %v", err)
	}
	if _, err := pgdb.Open(ctx, "not an address", "sales", migrations("t")); err == nil {
		t.Error("bad address accepted")
	}
}
