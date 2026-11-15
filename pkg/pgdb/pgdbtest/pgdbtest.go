// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package pgdbtest gives the tests of the register services an empty PostgreSQL database
// (ABAROW_TEST_POSTGRES; the test is skipped if it is not set).
package pgdbtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver
)

var counter atomic.Int64

// URL creates an empty database for the test and returns its connection address.
// The database is dropped when the test ends.
func URL(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("no ABAROW_TEST_POSTGRES; PostgreSQL tests skipped")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("abarow_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), counter.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil && !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("could not drop the test database: %v", err)
		}
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
