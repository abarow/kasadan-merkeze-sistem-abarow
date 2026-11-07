// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgrestest sets up an empty database for the tests that use the stock
// service's PostgreSQL adapter (ABAROW_TEST_POSTGRES; if it is missing the test is
// skipped).
package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"abarow.com/services/inventory/internal/adapters/out/postgres"
)

// Open sets up an empty database for each test and applies the stock
// migrations.
func Open(t testing.TB) *postgres.Store {
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
	name := fmt.Sprintf("abarow_stock_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	store, err := postgres.Open(ctx, stdlib.RegisterConnConfig(cfg))
	if err != nil {
		admin.ExecContext(ctx, "DROP DATABASE "+name)
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return store
}
